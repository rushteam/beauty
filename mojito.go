package mojito

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/rushteam/mojito/pkg/signals"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

//Service ..
type Service interface {
	Options() ServiceOptions
	Start() error
	Close(context.Context) error
}

//ServiceOptions ..
type ServiceOptions interface {
	ID() string
	Name() string
}

//App ..
type App struct {
	ctx     context.Context
	logger  *zap.Logger
	hooks   map[string][]func(*App)
	service []Service
	//shutdownTimeout timeout will be forces stop
	shutdownTimeout time.Duration
	shutdowning     sync.Locker

	quit chan struct{}
}

//AppOptions ..
type AppOptions func(app *App)

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
		shutdownTimeout: time.Second * 2,
		quit:            make(chan struct{}),
	}
	for _, opt := range opts {
		opt(app)
	}
	return app
}

// Run ..
func (app *App) Run(service ...Service) error {
	app.waitSignals()
	var eg errgroup.Group
	app.runHooks("before_run")
	app.service = append(app.service, service...)
	for _, srv := range app.service {
		func(srv Service) {
			eg.Go(func() error {
				return srv.Start()
			})
			app.logger.Debug("start", zap.String("service", srv.Options().ID()))
		}(srv)
	}
	app.runHooks("after_run")
	go func() {
		if err := eg.Wait(); err != nil {
			app.Shutdown()
		}
		app.logger.Debug("grace shutdown")
		close(app.quit)
	}()
	defer app.logger.Sync()
	<-app.quit
	return nil
}

// Shutdown ...
func (app *App) Shutdown() error {
	app.shutdowning.Lock()
	defer app.shutdowning.Unlock()
	ctx, cancel := context.WithTimeout(app.ctx, app.shutdownTimeout)
	// defer cancel()
	pid := os.Getpid()
	app.logger.Debug("shutdown", zap.Int("pid", pid), zap.String("timeout", app.shutdownTimeout.String()))
	var eg errgroup.Group
	for _, srv := range app.service {
		func(srv Service) {
			eg.Go(func() error {
				err := srv.Close(ctx)
				if err != nil {
					app.logger.Debug("service close", zap.String("service", srv.Options().ID()), zap.String("err", err.Error()))
				}
				return nil
			})
		}(srv)
	}
	eg.Go(func() error {
		defer func() {
			app.logger.Debug("timeout shutdown")
			close(app.quit)
		}()
		<-ctx.Done()
		cancel()
		return nil
	})
	return eg.Wait()
}

// waitSignals wait signal
func (app *App) waitSignals() {
	app.logger.Debug("init listen signal")
	signals.Shutdown(func() {
		err := app.Shutdown()
		if err != nil {
			app.logger.Debug(err.Error())
		}
	})
}
