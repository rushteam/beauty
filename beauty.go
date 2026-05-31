package beauty

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

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
	ready    atomic.Int32
	hooks    map[HookEvent][]HookFunc
	services []Service
	registry []discover.Registry
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
	srvCtx, cancel := context.WithCancel(ctx)

	// registry goroutine：等服务就绪后注册，ctx 取消后注销
	wg.Add(1)
	go func(srv Service) {
		defer wg.Done()
		defer cancel()

		if n, ok := srv.(ReadyNotifier); ok {
			select {
			case <-n.Ready():
			case <-srvCtx.Done():
				return
			}
		}
		if v, ok := srv.(discover.Service); ok {
			for _, r := range s.registry {
				stop, err := r.Register(srvCtx, v)
				if err != nil {
					logger.Error("service registry.Register error", "error", err)
					return
				}
				defer stop()
			}
		}
		<-srvCtx.Done()
	}(srv)

	// serve goroutine：运行服务，退出时取消 srvCtx 通知注册 goroutine 注销，
	// 同时触发 appCancel 让整个 app 进入 shutdown 流程。
	wg.Add(1)
	go func(srv Service) {
		defer wg.Done()
		defer cancel()
		defer appCancel()
		if err := srv.Start(srvCtx); err != nil {
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
