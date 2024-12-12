package retry

import (
	"context"
	"time"
)

type RetryOption func(*Options)

type Options struct {
	attempts int
	delay    time.Duration
	// maxDelay time.Duration
	backoff func(attempt int) time.Duration
}

func WithAttempts(n int) RetryOption {
	return func(o *Options) {
		o.attempts = n
	}
}

func Do(ctx context.Context, fn func() error, opts ...RetryOption) error {
	options := &Options{
		attempts: 3,
		delay:    time.Second,
		backoff: func(attempt int) time.Duration {
			return time.Duration(1<<uint(attempt)) * time.Second
		},
	}
	for _, opt := range opts {
		opt(options)
	}

	var err error
	for i := 0; i < options.attempts; i++ {
		if err = fn(); err == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(options.backoff(i)):
			continue
		}
	}
	return err
}
