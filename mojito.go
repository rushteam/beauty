package mojito

import (
	"context"
	"os"
	"time"

	"github.com/rushteam/mojito/pkg/lifecycle"
	"github.com/rushteam/mojito/pkg/log"
	"github.com/rushteam/mojito/pkg/registry"
	"github.com/rushteam/mojito/pkg/signals"
	"go.uber.org/zap"
)

//Service ..
type Service interface {
	Options() *Options
	Start() error
	Close(context.Context) error
}

//AppOptions ..
type AppOptions func(app *App)

//HookFunc ..
type HookFunc func(*App)

//App ..
type App struct {
	ctx      context.Context
	logger   *zap.Logger
	hooks    map[string][]HookFunc
	service  []Service
	registry registry.Registry
	//shutdownTimeout timeout will be forces stop
	shutdownTimeout time.Duration
	quit            chan struct{}
	cycle           *lifecycle.Cycle
}

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
	app := &App{
		ctx:             context.Background(),
		hooks:           make(map[string][]HookFunc),
		shutdownTimeout: time.Second * 2,
		quit:            make(chan struct{}),
		cycle:           lifecycle.NewCycle(),
	}
	app.SetLogger(log.DefaultLogger)
	reg, _ := registry.LoadEtcdRegistry()
	app.SetRegistry(reg)
	for _, opt := range opts {
		opt(app)
	}
	return app
}

//SetLogger ...
func (app *App) SetLogger(l *zap.Logger) {
	app.logger = l
}

//SetRegistry ...
func (app *App) SetRegistry(r registry.Registry) {
	app.registry = r
}

// Run ..
func (app *App) Run(service ...Service) error {
	app.service = service
	app.waitSignals()
	app.runHooks("before_start")
	for _, srv := range app.service {
		func(srv Service) {
			app.cycle.Run(func() error {
				//Register service
				if err := app.registry.Register(context.TODO(), srv.Options(), 5*time.Second); err != nil {
					app.logger.Error("register error", zap.String("service", srv.Options().Name), zap.Error(err))
				}
				//Deregister service
				defer func() {
					if err := app.registry.Deregister(context.TODO(), srv.Options()); err != nil {
						app.logger.Error("deregister error", zap.String("service", srv.Options().Name), zap.Error(err))
					}
				}()
				return srv.Start()
			})
			app.logger.Info("start", zap.String("service", srv.Options().Name))
		}(srv)
	}
	app.runHooks("after_start")
	defer app.logger.Sync()
	err := <-app.cycle.Wait()
	if err != nil {
		app.logger.Error("exit error", zap.Error(err))
	}
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
			app.cycle.Run(func() error {
				return srv.Close(ctx)
			})
		}(srv)
	}
	select {
	case <-app.cycle.Done():
		app.logger.Info("grace shutdown")
		//正常结束
	case <-ctx.Done():
		//超时
		app.logger.Info("timeout shutdown")
	}
	app.cycle.Close()
	return
}

// waitSignals wait signal
func (app *App) waitSignals() {
	app.logger.Info("init listen signal")
	signals.Shutdown(func() {
		app.Shutdown()
	})
}
