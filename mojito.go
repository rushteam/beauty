package mojito

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

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
	logger  *zap.Logger
	hooks   map[string][]func(*App)
	service []Service
	//shutdownTimeout timeout will be forces stop
	shutdownTimeout time.Duration
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
		logger:          logger,
		shutdownTimeout: time.Second * 5,
	}
	for _, opt := range opts {
		opt(app)
	}
	return app
}

// Run ..
func (app *App) Run(service ...Service) error {
	waitSignals(app)
	var eg errgroup.Group
	app.runHooks("before_run")
	app.service = append(app.service, service...)
	for _, srv := range app.service {
		app.logger.Debug("start", zap.String("service", srv.Options().Name()))
		eg.Go(func() error {
			return srv.Start()
		})
		defer func(srv Service) {
			app.logger.Debug("close", zap.String("service", srv.Options().Name()))
		}(srv)
	}
	app.runHooks("after_run")
	return eg.Wait()
}

// Shutdown ..
func (app *App) Shutdown() error {
	var eg errgroup.Group
	ctx, cancel := context.WithTimeout(context.Background(), app.shutdownTimeout)
	defer cancel()
	for _, srv := range app.service {
		eg.Go(func() error {
			srv.Close(ctx)
			return nil
		})
	}
	defer app.logger.Sync()
	return eg.Wait()
}

func waitSignals(app *App) {
	sig := make(chan os.Signal)
	signal.Notify(
		sig,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT,
		syscall.SIGUSR1,
		syscall.SIGUSR2,
		// syscall.SIGSTOP,
		// syscall.SIGKILL,
	)
	go func() {
		app.logger.Debug("init listen signal")
		select {
		case s := <-sig:
			switch s {
			case syscall.SIGQUIT:
				app.logger.Debug("listen signal", zap.String("mod", "signal"), zap.String("signal", "SIGQUIT"))
				_ = app.Shutdown() // graceful stop
			case syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL, syscall.SIGSTOP:
				_ = app.Shutdown() // terminate now
			}
		}
	}()
	time.Sleep(time.Microsecond) //sleep 1 micro second for frist listen signal
}
