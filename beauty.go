package beauty

import (
	"context"
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

// var _ registry.Service = (*Options)(nil)

//Service ..
type Service interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	String() string
	// Service() *registry.Service
}

//App ..
type App struct {
	ctx    context.Context
	logger *zap.Logger
	hooks  map[HookEvent][]HookFunc

	services []Service

	// registry registry.Registry
	shutdownTimeout time.Duration

	cycle *lifecycle.Cycle
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
		ctx:             context.Background(),
		cycle:           lifecycle.New(),
		hooks:           make(map[HookEvent][]HookFunc),
		shutdownTimeout: time.Second * 2,
	}
	app.SetLogger(log.Logger)
	for _, opt := range opts {
		opt(app)
	}

	return app
}

//SetLogger ...
func (app *App) SetLogger(l *zap.Logger) {
	if app.logger == nil {
		app.logger = l
	}
}

//SetRegistry ...
// func (app *App) SetRegistry(r registry.Registry) {
// 	app.registry = r
// }

// Run ..
func (app *App) Run(services ...Service) error {
	// reg, _ := registry.Build()
	// app.SetRegistry(reg)
	app.waitSignals()
	app.services = append(app.services, services...)
	app.runHooks(EventBeforeRun)
	for _, srv := range app.services {
		func(srv Service) {
			app.cycle.Run(func() error {
				//Register service
				// if err := app.registry.Register(context.TODO(), srv.Service(), 5*time.Second); err != nil {
				// 	app.logger.Error("register error", zap.String("service", srv.Service().String()), zap.Error(err))
				// }
				//Deregister service
				// defer func() {
				// 	if err := app.registry.Deregister(context.TODO(), srv.Service()); err != nil {
				// 		app.logger.Error("deregister error", zap.String("service", srv.Service().String()), zap.Error(err))
				// 	}
				// }()
				return srv.Start(app.ctx)
			})
			app.logger.Info("service start", zap.String("name", srv.String()))
		}(srv)
	}
	app.runHooks(EventAfterRun)
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
	app.logger.Info("shutdown", zap.Int("pid", os.Getpid()), zap.String("timeout", app.shutdownTimeout.String()))
	for _, srv := range app.services {
		func(srv Service) {
			app.cycle.Run(func() error {
				return srv.Stop(ctx)
			})
			app.logger.Info("service stop", zap.String("name", srv.String()))
		}(srv)
	}
	select {
	case <-app.cycle.Done():
		app.logger.Info("grace shutdown")
		//正常结束
	case <-ctx.Done():
		//超时
		app.logger.Warn("timeout shutdown")
	}
	app.cycle.Close()
}

func (app *App) waitSignals() {
	// go func() {
	// 	stop := make(chan struct{})
	// 	signals.Shutdown(func() {
	// 		close(stop)
	// 	})
	// 	<-stop
	// 	app.Shutdown()
	// }()
	signals.Shutdown(func() {
		app.Shutdown()
	})
}
