package beauty

import (
	"context"
	"os"
	"time"

	"github.com/rushteam/beauty/pkg/lifecycle"
	"github.com/rushteam/beauty/pkg/log"
	"github.com/rushteam/beauty/pkg/registry"
	"github.com/rushteam/beauty/pkg/signals"
	"go.uber.org/zap"
)

const (
	//StageBeforeRun ..
	StageBeforeRun = iota
	//StageAfterRun ..
	StageAfterRun
)

var initHookStages []int

//HookFunc ..
type HookFunc func(app *App)

//AppOptions ..
type AppOptions func(app *App)

//App ..
type App struct {
	ctx      context.Context
	logger   *zap.Logger
	hooks    map[int][]HookFunc
	service  []Service
	registry registry.Registry
	//shutdownTimeout timeout will be forces stop
	shutdownTimeout time.Duration
	quit            chan struct{}
	cycle           *lifecycle.Cycle
}

//Hook add a hook func to stage
func (app *App) Hook(stage int, fn HookFunc) {
	app.hooks[stage] = append(app.hooks[stage], fn)
}
func (app *App) runHooks(stage int) {
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
		hooks:           make(map[int][]HookFunc),
		shutdownTimeout: time.Second * 2,
		quit:            make(chan struct{}),
		cycle:           lifecycle.NewCycle(),
	}
	app.SetLogger(log.Logger)
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
	reg, _ := registry.Build()
	app.SetRegistry(reg)
	app.service = service
	app.waitSignals()
	app.runHooks(StageBeforeRun)
	for _, srv := range app.service {
		func(srv Service) {
			app.cycle.Run(func() error {
				//Register service
				if err := app.registry.Register(context.TODO(), srv.Service(), 5*time.Second); err != nil {
					app.logger.Error("register error", zap.String("service", srv.Service().String()), zap.Error(err))
				}
				//Deregister service
				defer func() {
					if err := app.registry.Deregister(context.TODO(), srv.Service()); err != nil {
						app.logger.Error("deregister error", zap.String("service", srv.Service().String()), zap.Error(err))
					}
				}()
				return srv.Start(context.Background())
			})
			app.logger.Info("start", zap.String("service", srv.Service().String()))
		}(srv)
	}
	app.runHooks(StageAfterRun)
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
	app.logger.Info("shutdown", zap.Int("pid", pid), zap.String("timeout", app.shutdownTimeout.String()))
	for _, srv := range app.service {
		func(srv Service) {
			app.cycle.Run(func() error {
				return srv.Stop(ctx)
			})
			app.logger.Info("stop", zap.String("service", srv.Service().String()))
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
