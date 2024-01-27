package signals

import (
	"context"
	"os"
	"os/signal"

	"github.com/rushteam/beauty/pkg/logger"
)

// Shutdown ...
func NotifyShutdownContext(ctx context.Context, f func()) context.Context {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		c := make(chan os.Signal, 2)
		signal.Notify(c, shutdownSignals...)
		defer signal.Stop(c)
		select {
		case <-ctx.Done():
		case sig := <-c:
			logger.Info("stoping with signal", "signal", sig.String())
			f()
			cancel()
			<-c
			os.Exit(1) // second signal. Exit directly.
		}
	}()
	return ctx
}
