package beauty

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rushteam/beauty/pkg/service/core"
	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/service/logger"
	"github.com/rushteam/beauty/pkg/service/telemetry"
	"github.com/rushteam/beauty/pkg/signals"
	"github.com/rushteam/beauty/pkg/xgo"
)

var (
	gpoolOnce sync.Once
	gpool     xgo.Pool
)

func getPool() xgo.Pool {
	gpoolOnce.Do(func() { gpool = xgo.New() })
	return gpool
}

type HookEvent int

const (
	//EventBeforeRun ..
	EventBeforeRun HookEvent = iota
	//EventAfterRun ..
	EventAfterRun
)

// HookFunc ..
type HookFunc func(app *App)

// Option ..
type Option func(app *App)

// WithService ..
func WithService(s Service) Option {
	return func(app *App) {
		app.services = append(app.services, s)
	}
}

func WithRegistry(r discover.Registry) Option {
	return func(app *App) {
		if r != nil {
			app.registry = append(app.registry, r)
		}
	}
}

// WithShutdownDrainDelay 设置优雅退出时"先注销、再停服"之间的排空等待。
// 关闭时框架会先从注册中心注销实例，等待该时长让客户端/负载均衡感知到下线，
// 再开始停止服务端，从而避免关闭瞬间仍有新请求被路由到本实例（滚动发布零停机）。
// 默认 0：仍保证"先注销后停服"的顺序，只是不额外等待。
// 仅对实现了 discover.Service 并配置了 registry 的服务生效。
func WithShutdownDrainDelay(d time.Duration) Option {
	return func(app *App) {
		app.drainDelay = d
	}
}

func WithComponent(c core.Component) Option {
	return func(app *App) {
		cancel := c.Init()
		logger.Info(fmt.Sprintf("component %s inited", c.Name()))
		app.Hook(EventAfterRun, func(app *App) {
			defer cancel()
			logger.Info(fmt.Sprintf("component %s stopping...", c.Name()))
		})
	}
}

func WithTrace(opts ...telemetry.TraceOption) Option {
	return WithComponent(telemetry.NewTracer(opts...))
}

func WithMetric(opts ...telemetry.MetricOption) Option {
	return WithComponent(telemetry.NewMetric(opts...))
}

// Service ..
type Service interface {
	Start(ctx context.Context) error
	String() string
}

// ReadyNotifier is an optional interface a Service can implement to signal
// that it is ready to accept traffic (e.g. port is listening).
// startService waits for this signal before registering with the registry.
type ReadyNotifier interface {
	Ready() <-chan struct{}
}
type ServiceKind interface {
	Kind() string
}

// App ..
type App struct {
	ready      atomic.Int32
	hooks      map[HookEvent][]HookFunc
	services   []Service
	registry   []discover.Registry
	drainDelay time.Duration
}

// Hook add a hook func to stage
func (app *App) Hook(stage HookEvent, fn HookFunc) {
	app.hooks[stage] = append(app.hooks[stage], fn)
}

func (app *App) runHooks(stage HookEvent) {
	if hooks, ok := app.hooks[stage]; ok {
		for _, h := range hooks {
			h(app)
		}
	}
}

// New ..
func New(opts ...Option) *App {
	s := &App{
		hooks: make(map[HookEvent][]HookFunc),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Start ..
func (s *App) Start(ctx context.Context) error {
	if s.ready.Load() == 1 {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	signals.NotifyShutdownContext(ctx, func() {
		s.ready.Swap(0)
		cancel()
	})
	s.runHooks(EventBeforeRun)

	// svcWg 追踪所有 srv.Start() goroutine，shutdown 时等它们全部退出
	var svcWg sync.WaitGroup
	for _, srv := range s.services {
		s.startService(ctx, &svcWg, srv, cancel)
	}
	s.ready.Swap(1)

	// 等待 ctx 取消（signal、外部 cancel 或任意服务退出）
	<-ctx.Done()

	// 等待所有服务的 Start() 返回，确保 in-flight 请求处理完毕
	svcWg.Wait()

	s.runHooks(EventAfterRun)
	defer logger.Sync()
	return nil
}

func (s *App) startService(ctx context.Context, wg *sync.WaitGroup, srv Service, appCancel context.CancelFunc) {
	// serveCtx 单独控制 server 何时停止，由下面的编排 goroutine 在"注销 + 排空"之后才取消，
	// 而不是与注销同时发生——这样关闭顺序为：先注销 → 等客户端感知 → 再停 server。
	// 用 WithoutCancel 保留父 ctx 的 value（logger/trace 等），但不跟随父 ctx 取消，
	// 由 stopServe 决定停止时机；父 ctx 取消会经编排 goroutine 转化为 stopServe。
	serveCtx, stopServe := context.WithCancel(context.WithoutCancel(ctx))

	// 编排 goroutine：就绪后注册；shutdown 时按 注销 → 排空 → 停服 的顺序收尾。
	wg.Add(1)
	go func(srv Service) {
		defer wg.Done()
		defer stopServe() // 兜底：本 goroutine 任意路径退出都确保 server 被通知停止

		// 等待就绪；就绪前若已开始关停则直接退出
		if n, ok := srv.(ReadyNotifier); ok {
			select {
			case <-n.Ready():
			case <-ctx.Done():
				return
			case <-serveCtx.Done():
				return
			}
		}

		// 注册（用 app ctx，使租约/keepalive 跟随应用生命周期）
		var deregister func()
		if v, ok := srv.(discover.Service); ok {
			stops := make([]func(), 0, len(s.registry))
			for _, r := range s.registry {
				stop, err := r.Register(ctx, v)
				if err != nil {
					logger.Error("service registry.Register error", "error", err)
					for _, st := range stops {
						st()
					}
					return
				}
				stops = append(stops, stop)
			}
			if len(stops) > 0 {
				deregister = func() {
					for _, st := range stops {
						st()
					}
				}
			}
		}

		// 等待关停触发：app 正常 shutdown，或 server 自行退出（崩溃/返回）
		select {
		case <-ctx.Done():
		case <-serveCtx.Done():
			// server 已先退出，直接注销返回，无需排空
			if deregister != nil {
				deregister()
			}
			return
		}

		// 正常 shutdown：先注销，给客户端/LB 留出感知窗口，再停 server
		if deregister != nil {
			deregister()
			if s.drainDelay > 0 && serveCtx.Err() == nil {
				select {
				case <-time.After(s.drainDelay):
				case <-serveCtx.Done():
				}
			}
		}
		stopServe()
	}(srv)

	// serve goroutine：运行服务；退出时触发 appCancel 让整个 app 进入 shutdown。
	wg.Add(1)
	go func(srv Service) {
		defer wg.Done()
		defer appCancel()
		defer stopServe()
		if err := srv.Start(serveCtx); err != nil {
			logger.Error("service start error", "error", err)
		}
	}(srv)
}

func (s *App) Ready() bool {
	return s.ready.Load() == 1
}

func Go(f func()) {
	getPool().Go(f)
}
