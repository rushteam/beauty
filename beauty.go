package beauty

import (
	"context"
	"net"
	"os"
	"time"

	"github.com/rushteam/beauty/pkg/lifecycle"
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

//HookFunc ..
type HookFunc func(app *App)

//AppOption ..
type AppOption func(app *App)

func WithServer(s Service) AppOption {
	return func(app *App) {
		app.services = append(app.services, s)
	}
}

// var _ registry.Service = (*Options)(nil)

//Service ..
type Service interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	String() string
}

//App ..
type App struct {
	ctx             context.Context
	hooks           map[HookEvent][]HookFunc
	services        []Service
	shutdownTimeout time.Duration
	cycle           *lifecycle.Cycle
}

//Hook add a hook func to stage
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

//New ..
func New(opts ...AppOption) *App {
	app := &App{
		cycle:           lifecycle.New(),
		hooks:           make(map[HookEvent][]HookFunc),
		shutdownTimeout: time.Second * 2,
	}
	for _, opt := range opts {
		opt(app)
	}

	return app
}

// Run ..
func (app *App) Run(services ...Service) error {
	app.waitSignals()
	app.services = append(app.services, services...)
	app.runHooks(EventBeforeRun)
	for _, srv := range app.services {
		func(srv Service) {
			app.cycle.Run(func() error {
				return srv.Start(app.Context())
			})
			log.Info("service start", zap.String("name", srv.String()))
		}(srv)
	}
	app.runHooks(EventAfterRun)
	defer log.Sync()
	err := <-app.cycle.Wait()
	if err != nil {
		log.Error("exit error", zap.Error(err))
	}
	return nil
}

func (app *App) Context() context.Context {
	if app.ctx == nil {
		app.ctx = context.Background()
	}
	return app.ctx
}

// Shutdown ...
func (app *App) Shutdown() {
	ctx, cancel := context.WithTimeout(app.Context(), app.shutdownTimeout)
	defer cancel()
	log.Info("shutdown", zap.Int("pid", os.Getpid()), zap.String("timeout", app.shutdownTimeout.String()))
	for _, srv := range app.services {
		func(srv Service) {
			app.cycle.Run(func() error {
				return srv.Stop(ctx)
			})
			log.Info("service stop", zap.String("name", srv.String()))
		}(srv)
	}
	select {
	case <-app.cycle.Done():
		log.Info("grace shutdown")
		//正常结束
	case <-ctx.Done():
		//超时
		log.Warn("timeout shutdown")
	}
	app.cycle.Close()
}

func (app *App) Listen(addr string) net.Listener {
	log.Info("listen", zap.String("addr", addr))
	var lc net.ListenConfig
	ln, err := lc.Listen(app.Context(), "tcp", addr)
	if err != nil {
		log.Fatal("listen error", zap.Error(err))
		return nil
	}
	return ln
}

func (app *App) waitSignals() {
	signals.Shutdown(func() {
		app.Shutdown()
	})
}
