package beauty

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rushteam/beauty/pkg/core"
	"github.com/rushteam/beauty/pkg/discover"
	"github.com/rushteam/beauty/pkg/logger"
	"github.com/rushteam/beauty/pkg/signals"
	"github.com/rushteam/beauty/pkg/tracing"
)

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
	ctx, cancel := context.WithCancel(ctx)
	signals.NotifyShutdownContext(ctx, func() {
		cancel()
	})
	s.runHooks(EventBeforeRun)
	wg := sync.WaitGroup{}
	for _, srv := range s.services {
		wg.Add(1)
		go func(srv Service) {
			defer wg.Done()
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
			if err := srv.Start(ctx); err != nil {
				logger.Error("service start error", "error", err)
			}
		}(srv)
	}
	wg.Wait()
	<-ctx.Done()
	s.runHooks(EventAfterRun)
	defer logger.Sync()
	sleep := time.Millisecond*100 + time.Second*time.Duration(len(s.registry))
	logger.Info(fmt.Sprintf("stop after %s", sleep))
	time.Sleep(sleep)
	return nil
}

func Go(f func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error(fmt.Sprintf("panic recovered: %v", r))
			}
		}()
		f()
	}()
}
