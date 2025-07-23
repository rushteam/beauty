package beauty

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rushteam/beauty/pkg/core"
	"github.com/rushteam/beauty/pkg/discover"
	"github.com/rushteam/beauty/pkg/logger"
	"github.com/rushteam/beauty/pkg/signals"
	"github.com/rushteam/beauty/pkg/tracing"
	"github.com/rushteam/beauty/pkg/xgo"
)

var gpool xgo.Pool

func init() {
	gpool = xgo.New()
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

// type ServiceOption func(*ServiceContext)
type ServiceOption func(*discover.ServiceInfo)

func WithServiceName(name string) ServiceOption {
	return func(s *discover.ServiceInfo) {
		s.Name = name
	}
}

func WithServiceMeta(k, v string) ServiceOption {
	return func(s *discover.ServiceInfo) {
		s.Metadata[k] = v
	}
}

// WithService ..
func WithService(s Service, opts ...ServiceOption) Option {
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

func WithTrace(opts ...tracing.TraceOption) Option {
	return WithComponent(tracing.NewTracer(opts...))
}

func WithMetric(opts ...tracing.MetricOption) Option {
	return WithComponent(tracing.NewMetric(opts...))
}

// Service ..
type Service interface {
	Start(ctx context.Context) error
	String() string
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
	wg := sync.WaitGroup{}
	for _, srv := range s.services {
		s.startService(ctx, &wg, srv)
	}
	s.ready.Swap(1)
	wg.Wait()
	<-ctx.Done()
	s.runHooks(EventAfterRun)
	defer logger.Sync()
	sleep := time.Millisecond*100 + time.Second*time.Duration(len(s.registry))
	logger.Info(fmt.Sprintf("stop after %s", sleep))
	time.Sleep(sleep)
	return nil
}

func (s *App) startService(ctx context.Context, wg *sync.WaitGroup, srv Service) {
	wg.Add(1)
	ctx, cancel := context.WithCancel(ctx)
	go func(srv Service) {
		defer wg.Done()
		select {
		case <-time.After(time.Microsecond * 10):
			if v, ok := srv.(discover.Service); ok {
				for _, r := range s.registry {
					// TODO: 这里不能超时,需要在Register内部做，因为里面需要做 keepalive
					stop, err := r.Register(ctx, v)
					if err != nil {
						logger.Error("service registry.Register error", "error", err)
						return
					}
					defer stop()
				}
			}
			<-ctx.Done()
			return
		case <-ctx.Done():
			return
		}
	}(srv)
	go func(srv Service) {
		if err := srv.Start(ctx); err != nil {
			logger.Error("service start error", "error", err)
		}
		cancel()
	}(srv)
}

func (s *App) Ready() bool {
	return s.ready.Load() == 1
}

func Go(f func()) {
	gpool.Go(f)
}
