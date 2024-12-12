package beauty

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/rushteam/beauty/pkg/core"
	"github.com/rushteam/beauty/pkg/core/dependency"
	"github.com/rushteam/beauty/pkg/discover"
	"github.com/rushteam/beauty/pkg/logger"
	"github.com/rushteam/beauty/pkg/service/grpcserver"
	"github.com/rushteam/beauty/pkg/service/webserver"
	"github.com/rushteam/beauty/pkg/signals"
	"github.com/rushteam/beauty/pkg/tracing"
	"google.golang.org/grpc"
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
	hooks      map[HookEvent][]HookFunc
	services   []Service
	registry   []discover.Registry
	depManager *dependency.DependencyManager
}

func (app *App) RegisterDependency(name string, deps []string, readyFunc func() bool) {
	app.depManager.Register(name, deps, readyFunc)
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
		hooks:      make(map[HookEvent][]HookFunc),
		depManager: dependency.NewManager(),
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
	// 等待依赖就绪
	if err := s.depManager.WaitForDependencies(ctx); err != nil {
		return fmt.Errorf("dependency check failed: %w", err)
	}
	s.runHooks(EventBeforeRun)
	wg := sync.WaitGroup{}
	for _, srv := range s.services {
		s.startService(ctx, &wg, srv)
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

// GRPC adds a gRPC server to the app
func (app *App) GRPC(addr string, handler func(*grpc.Server), opts ...grpcserver.Options) *App {
	srv := grpcserver.New(addr, handler, opts...)
	app.services = append(app.services, srv)
	return app
}

// HTTP adds an HTTP server to the app
func (app *App) HTTP(addr string, mux http.Handler, opts ...webserver.Options) *App {
	//handler func(*http.Server)
	srv := webserver.New(addr, mux, opts...)
	app.services = append(app.services, srv)
	return app
}
