package signals

import (
	"context"
	"os"
	"os/signal"
	"time"

	"github.com/rushteam/beauty/pkg/service/logger"
)

// shutdownTimeout 是 shutdown hook 的最大执行时间,超过后强制 cancel ctx。
const shutdownTimeout = 30 * time.Second

// Shutdown with first signal, second signal exit directly
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
			hookDone := make(chan struct{})
			go func() {
				defer func() {
					if r := recover(); r != nil {
						logger.Error("panic in shutdown hook", "panic", r)
					}
					close(hookDone)
					cancel()
				}()
				f()
			}()
			timer := time.NewTimer(shutdownTimeout)
			select {
			case <-hookDone:
				timer.Stop()
				return
			case <-timer.C:
				logger.Error("shutdown hook timed out, forcing cancel", "timeout", shutdownTimeout)
				cancel()
			case <-c:
				logger.Info("second signal forced stop")
				os.Exit(1)
			}
		}
	}()
	return ctx
}
