package beauty

import (
	"context"
	"sync"
	"time"

	"github.com/rushteam/beauty/pkg/discover"
	"github.com/rushteam/beauty/pkg/logger"
	"github.com/rushteam/beauty/pkg/signals"
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

type ServiceTag func(*ServiceInfo)

type ServiceInfo struct {
	Service
	Name string
	Addr string
	Tags map[string]string
}

func (s ServiceInfo) ServiceName() string {
	return s.Name
}

func WithServiceName(name string) ServiceTag {
	return func(t *ServiceInfo) {
		t.Name = name
	}
}

func WithServiceTag(k, v string) ServiceTag {
	return func(t *ServiceInfo) {
		t.Tags[k] = v
	}
}

// WithService ..
func WithService(s Service, tags ...ServiceTag) Option {
	si := &ServiceInfo{
		Service: s,
		Name:    s.String(),
		Tags:    make(map[string]string, 0),
	}
	for _, tag := range tags {
		tag(si)
	}
	return func(app *App) {
		app.services = append(app.services, si)
	}
}
func WithRegistry(r discover.Registry) Option {
	return func(app *App) {
		app.registry = append(app.registry, r)
	}
}

// Service ..
type Service interface {
	Start(ctx context.Context) error
	String() string
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

// AppendService ..
// func (s *App) AppendService(services ...Service) {
// 	s.services = append(s.services, services...)
// }

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
			defer func() {
				for _, r := range s.registry {
					if v, ok := srv.(discover.Endpoint); ok {
						r.Deregister(v)
					}
				}
				wg.Done()
			}()
			//register
			for _, r := range s.registry {
				if v, ok := srv.(discover.Endpoint); ok {
					r.Register(v)
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
	time.Sleep(time.Millisecond * 100)
	return nil
}
