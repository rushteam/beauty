package mojito

import (
	"context"
	"os"
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
	quit            chan struct{}
	eg              *errgroup.Group
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
		eg:              &errgroup.Group{},
	}
	for _, opt := range opts {
		opt(app)
	}
	return app
}

func (app *App) start(fn func() error) {
	app.eg.Go(fn)
}
func (app *App) wait() <-chan error {
	errCh := make(chan error)
	go func() {
		if err := app.eg.Wait(); err != nil {
			errCh <- err
		}
		close(errCh)
	}()
	return errCh
}

// Run ..
func (app *App) Run(service ...Service) error {
	app.service = service
	app.waitSignals()
	app.runHooks("before_start")
	for _, srv := range app.service {
		func(srv Service) {
			app.start(func() error {
				return srv.Start()
			})
			app.logger.Debug("start", zap.String("service", srv.Options().ID()))
		}(srv)
	}
	app.runHooks("after_start")
	defer app.logger.Sync()
	<-app.quit
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
			app.start(func() error {
				err := srv.Close(ctx)
				if err != nil {
					app.logger.Debug("service close fail", zap.String("service", srv.Options().ID()), zap.String("err", err.Error()))
				}
				return nil
			})
		}(srv)
	}
	select {
	case <-app.wait():
		app.logger.Debug("grace shutdown")
		close(app.quit)
		//正常结束
	case <-ctx.Done():
		//超时
		app.logger.Debug("timeout shutdown")
		close(app.quit)
	}
	return
}

// waitSignals wait signal
func (app *App) waitSignals() {
	app.logger.Debug("init listen signal")
	signals.Shutdown(func() {
		app.Shutdown()
	})
}
