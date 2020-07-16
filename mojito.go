package mojito

import (
	"context"
	"os"
	"time"

	"github.com/rushteam/mojito/pkg/lifecycle"
	"github.com/rushteam/mojito/pkg/signals"
	"go.uber.org/zap"
)

//Service ..
type Service interface {
	Options() ServiceOptions
	Start() error
	Close(context.Context) error
}

//ServiceOptions ..
type ServiceOptions interface {
	UUID() string
	Name() string
	Version() string
	Metadata() map[string]string
}

//App ..
type App struct {
	ctx     context.Context
	logger  *zap.Logger
	hooks   map[string][]HookFunc
	service []Service
	//shutdownTimeout timeout will be forces stop
	shutdownTimeout time.Duration
	quit            chan struct{}
	lc              *lifecycle.Cycle
}

//AppOptions ..
type AppOptions func(app *App)

//HookFunc ..
type HookFunc func(*App)

//AddHook add a hook func to stage
func (app *App) AddHook(stage string, fn HookFunc) {
	app.hooks[stage] = append(app.hooks[stage], fn)
}
func (app *App) runHooks(stage string) {
	if hooks, ok := app.hooks[stage]; ok {
		for _, h := range hooks {
			h(app)
		}
	}
}

//Init ..
func Init(opts ...AppOptions) *App {
	logger, _ := zap.NewDevelopment()
	app := &App{
		ctx:             context.Background(),
		logger:          logger,
		hooks:           make(map[string][]HookFunc),
		shutdownTimeout: time.Second * 2,
		quit:            make(chan struct{}),
		lc:              lifecycle.NewCycle(),
	}
	for _, opt := range opts {
		opt(app)
	}
	return app
}

// Run ..
func (app *App) Run(service ...Service) error {
	app.service = service
	app.waitSignals()
	app.runHooks("before_start")
	for _, srv := range app.service {
		func(srv Service) {
			app.lc.Run(func() error {
				return srv.Start()
			})
			app.logger.Info("start", zap.String("service", srv.Options().ID()))
		}(srv)
	}
	app.runHooks("after_start")
	defer app.logger.Sync()
	<-app.lc.Wait()
	return nil
}

// Shutdown ...
func (app *App) Shutdown() {
	ctx, cancel := context.WithTimeout(app.ctx, app.shutdownTimeout)
	defer cancel()
	pid := os.Getpid()
	app.logger.Debug("shutdown", zap.Int("pid", pid), zap.String("timeout", app.shutdownTimeout.String()))
	for _, srv := range app.service {
		func(srv Service) {
			app.lc.Run(func() error {
				err := srv.Close(ctx)
				if err != nil {
					app.logger.Error("service close fail", zap.String("service", srv.Options().ID()), zap.String("err", err.Error()))
				}
				return nil
			})
		}(srv)
	}
	select {
	case <-app.lc.Done():
		app.logger.Info("grace shutdown")
		app.lc.Close()
		//正常结束
	case <-ctx.Done():
		//超时
		app.logger.Info("timeout shutdown")
		app.lc.Close()
	}
	return
}

// waitSignals wait signal
func (app *App) waitSignals() {
	app.logger.Info("init listen signal")
	signals.Shutdown(func() {
		app.Shutdown()
	})
}
