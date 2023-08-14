package beauty

import (
	"context"

	"github.com/rushteam/beauty/pkg/log"
	"github.com/rushteam/beauty/pkg/signals"
	"go.uber.org/zap"
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

// WithService ..
func WithService(s ...Service) Option {
	return func(app *App) {
		app.services = append(app.services, s...)
	}
}
func WithLogger() Option {
	log.Logger, _ = zap.NewDevelopment()
	return func(app *App) {}
}

// var _ registry.Service = (*Options)(nil)

// Service ..
type Service interface {
	Start(ctx context.Context) error
	String() string
}

// App ..
type App struct {
	hooks    map[HookEvent][]HookFunc
	services []Service
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
func (s *App) AppendService(services ...Service) {
	s.services = append(s.services, services...)
}

// Start ..
func (s *App) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	// log.Info("app start", zap.String("start time", time.Now().Format("2006-01-02 15:04:05")))
	signals.NotifyShutdownContext(ctx, func() {
		cancel()
	})
	s.runHooks(EventBeforeRun)
	for _, srv := range s.services {
		func(srv Service) {
			srv.Start(ctx)
		}(srv)
	}
	<-ctx.Done()
	s.runHooks(EventAfterRun)
	cancel()
	defer log.Sync()
	return nil
}
