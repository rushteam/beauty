package backoff_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/backoff"
)

func TestDuration_ExponentialNoJitter(t *testing.T) {
	p := backoff.New(
		backoff.WithBase(100*time.Millisecond),
		backoff.WithFactor(2),
		backoff.WithJitter(backoff.JitterNone),
		backoff.WithMax(0), // 不封顶
	)
	want := []time.Duration{100, 200, 400, 800}
	for i, w := range want {
		got := p.Duration(i)
		if got != w*time.Millisecond {
			t.Fatalf("Duration(%d) = %v, want %v", i, got, w*time.Millisecond)
		}
	}
}

func TestDuration_Capped(t *testing.T) {
	p := backoff.New(
		backoff.WithBase(time.Second),
		backoff.WithFactor(10),
		backoff.WithJitter(backoff.JitterNone),
		backoff.WithMax(5*time.Second),
	)
	// 1s, 10s→cap 5s, 100s→cap 5s
	if p.Duration(0) != time.Second {
		t.Fatalf("Duration(0) = %v", p.Duration(0))
	}
	if p.Duration(1) != 5*time.Second {
		t.Fatalf("Duration(1) = %v, want 5s (capped)", p.Duration(1))
	}
	if p.Duration(5) != 5*time.Second {
		t.Fatalf("Duration(5) = %v, want 5s (capped)", p.Duration(5))
	}
}

func TestDuration_NoOverflow(t *testing.T) {
	p := backoff.New(
		backoff.WithBase(time.Second),
		backoff.WithFactor(2),
		backoff.WithJitter(backoff.JitterNone),
		backoff.WithMax(time.Minute),
	)
	// 大 attempt 不应 panic/负数,应被 cap 住。
	if got := p.Duration(1000); got != time.Minute {
		t.Fatalf("Duration(1000) = %v, want 1m (capped, no overflow)", got)
	}
}

func TestDuration_FullJitterBounds(t *testing.T) {
	p := backoff.New(
		backoff.WithBase(100*time.Millisecond),
		backoff.WithFactor(2),
		backoff.WithJitter(backoff.JitterFull),
		backoff.WithMax(0),
	)
	// attempt 3 名义 800ms;full jitter 应落在 [0, 800ms]。
	for range 200 {
		got := p.Duration(3)
		if got < 0 || got > 800*time.Millisecond {
			t.Fatalf("full jitter out of bounds: %v", got)
		}
	}
}

func TestDuration_EqualJitterBounds(t *testing.T) {
	p := backoff.New(
		backoff.WithBase(100*time.Millisecond),
		backoff.WithFactor(2),
		backoff.WithJitter(backoff.JitterEqual),
		backoff.WithMax(0),
	)
	// attempt 3 名义 800ms;equal jitter 应落在 [400ms, 800ms]。
	for range 200 {
		got := p.Duration(3)
		if got < 400*time.Millisecond || got > 800*time.Millisecond {
			t.Fatalf("equal jitter out of bounds: %v", got)
		}
	}
}

func TestRetry_SucceedsAfterFailures(t *testing.T) {
	p := backoff.New(
		backoff.WithBase(time.Millisecond),
		backoff.WithJitter(backoff.JitterNone),
		backoff.WithMaxRetries(5),
	)
	var calls int
	err := p.Retry(context.Background(), func(ctx context.Context) error {
		calls++
		if calls < 3 {
			return errors.New("transient")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("should succeed: %v", err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestRetry_ExhaustsAndReturnsLastErr(t *testing.T) {
	p := backoff.New(backoff.WithBase(time.Millisecond), backoff.WithMaxRetries(2))
	sentinel := errors.New("always")
	var calls int
	err := p.Retry(context.Background(), func(ctx context.Context) error {
		calls++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}
	if calls != 3 { // 1 + 2 retries
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestRetry_ContextCancel(t *testing.T) {
	p := backoff.New(backoff.WithBase(50*time.Millisecond), backoff.WithMaxRetries(10), backoff.WithJitter(backoff.JitterNone))
	ctx, cancel := context.WithCancel(context.Background())
	var calls int
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	err := p.Retry(ctx, func(ctx context.Context) error {
		calls++
		return errors.New("fail")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	// 应在退避等待期间被取消,不会跑满 11 次。
	if calls > 3 {
		t.Fatalf("calls = %d, expected to stop early on cancel", calls)
	}
}

func TestRetryIf_NonRetryableStops(t *testing.T) {
	p := backoff.New(backoff.WithBase(time.Millisecond), backoff.WithMaxRetries(10))
	fatal := errors.New("4xx do not retry")
	var calls int
	err := p.RetryIf(context.Background(), func(ctx context.Context) error {
		calls++
		return fatal
	}, func(e error) bool {
		return !errors.Is(e, fatal) // fatal 不重试
	})
	if !errors.Is(err, fatal) {
		t.Fatalf("err = %v", err)
	}
	if calls != 1 {
		t.Fatalf("non-retryable should stop immediately, calls = %d", calls)
	}
}

func TestConcurrentDuration(t *testing.T) {
	p := backoff.New()
	done := make(chan struct{})
	for range 50 {
		go func() {
			for range 1000 {
				_ = p.Duration(3)
			}
			done <- struct{}{}
		}()
	}
	for range 50 {
		<-done
	}
}
