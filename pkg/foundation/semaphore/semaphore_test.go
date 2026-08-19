package semaphore

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDo_Success(t *testing.T) {
	s := New(WithCapacity(5))
	err := s.Do(context.Background(), func() error { return nil })
	if err != nil {
		t.Fatalf("Do error: %v", err)
	}
}

func TestDo_PropagatesError(t *testing.T) {
	s := New(WithCapacity(5))
	want := errors.New("inner")
	err := s.Do(context.Background(), func() error { return want })
	if err != want {
		t.Fatalf("got %v, want %v", err, want)
	}
}

func TestDo_RejectsWhenFull(t *testing.T) {
	s := New(WithCapacity(2))
	ctx := context.Background()

	started := make(chan struct{}, 2)
	release := make(chan struct{})
	for i := 0; i < 2; i++ {
		go s.Do(ctx, func() error {
			started <- struct{}{}
			<-release
			return nil
		})
	}
	<-started
	<-started

	err := s.Do(ctx, func() error { return nil })
	if !errors.Is(err, ErrFull) {
		t.Fatalf("got %v, want ErrFull", err)
	}
	close(release)
}

func TestDoWithCost(t *testing.T) {
	s := New(WithCapacity(10))
	ctx := context.Background()

	started := make(chan struct{})
	release := make(chan struct{})
	go s.DoWithCost(ctx, 8, func() error {
		close(started)
		<-release
		return nil
	})
	<-started

	// 剩余 2,请求 3 应该被拒
	err := s.DoWithCost(ctx, 3, func() error { return nil })
	if !errors.Is(err, ErrFull) {
		t.Fatalf("got %v, want ErrFull", err)
	}

	// 请求 2 应该成功
	err = s.DoWithCost(ctx, 2, func() error { return nil })
	if err != nil {
		t.Fatalf("DoWithCost(2) error: %v", err)
	}

	close(release)
}

func TestTryAcquire(t *testing.T) {
	s := New(WithCapacity(3))

	if !s.TryAcquire(2) {
		t.Fatal("TryAcquire(2) should succeed")
	}
	if s.TryAcquire(2) {
		t.Fatal("TryAcquire(2) should fail (only 1 left)")
	}
	if !s.TryAcquire(1) {
		t.Fatal("TryAcquire(1) should succeed")
	}
	s.Release(3)
	if s.Available() != 3 {
		t.Fatalf("available = %d, want 3", s.Available())
	}
}

func TestAcquire_WaitsAndSucceeds(t *testing.T) {
	s := New(WithCapacity(1), WithMaxWait(200*time.Millisecond))
	ctx := context.Background()

	release := make(chan struct{})
	started := make(chan struct{})
	go s.Do(ctx, func() error {
		close(started)
		<-release
		return nil
	})
	<-started

	go func() {
		time.Sleep(30 * time.Millisecond)
		close(release)
	}()

	err := s.Do(ctx, func() error { return nil })
	if err != nil {
		t.Fatalf("Do with wait: %v", err)
	}
}

func TestAcquire_WaitTimeout(t *testing.T) {
	s := New(WithCapacity(1), WithMaxWait(20*time.Millisecond))
	ctx := context.Background()

	release := make(chan struct{})
	started := make(chan struct{})
	go s.Do(ctx, func() error {
		close(started)
		<-release
		return nil
	})
	<-started

	err := s.Do(ctx, func() error { return nil })
	if !errors.Is(err, ErrFull) {
		t.Fatalf("got %v, want ErrFull after timeout", err)
	}
	close(release)
}

func TestAcquire_ContextCancel(t *testing.T) {
	s := New(WithCapacity(1), WithMaxWait(time.Second))

	release := make(chan struct{})
	started := make(chan struct{})
	go s.Do(context.Background(), func() error {
		close(started)
		<-release
		return nil
	})
	<-started

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := s.Do(ctx, func() error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	close(release)
}

func TestOnReject(t *testing.T) {
	var rejectCount atomic.Int64
	s := New(
		WithCapacity(1),
		WithOnReject(func() { rejectCount.Add(1) }),
	)

	release := make(chan struct{})
	started := make(chan struct{})
	go s.Do(context.Background(), func() error {
		close(started)
		<-release
		return nil
	})
	<-started

	s.Do(context.Background(), func() error { return nil })
	s.Do(context.Background(), func() error { return nil })

	if rejectCount.Load() != 2 {
		t.Fatalf("reject count = %d, want 2", rejectCount.Load())
	}
	close(release)
}

func TestInFlight_And_Available(t *testing.T) {
	s := New(WithCapacity(10))

	if s.Available() != 10 {
		t.Fatalf("available = %d, want 10", s.Available())
	}
	if s.Capacity() != 10 {
		t.Fatalf("capacity = %d, want 10", s.Capacity())
	}

	s.Acquire(context.Background(), 3)
	s.Acquire(context.Background(), 4)

	if s.InFlight() != 7 {
		t.Fatalf("inflight = %d, want 7", s.InFlight())
	}
	if s.Available() != 3 {
		t.Fatalf("available = %d, want 3", s.Available())
	}

	s.Release(7)
}

func TestConcurrent(t *testing.T) {
	s := New(WithCapacity(10), WithMaxWait(100*time.Millisecond))
	ctx := context.Background()
	var wg sync.WaitGroup
	var maxSeen atomic.Int64

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Do(ctx, func() error {
				cur := s.InFlight()
				for {
					old := maxSeen.Load()
					if cur <= old || maxSeen.CompareAndSwap(old, cur) {
						break
					}
				}
				time.Sleep(time.Millisecond)
				return nil
			})
		}()
	}
	wg.Wait()

	if maxSeen.Load() > 10 {
		t.Fatalf("max concurrent = %d, exceeds capacity 10", maxSeen.Load())
	}
}

func TestConcurrent_Weighted(t *testing.T) {
	s := New(WithCapacity(10), WithMaxWait(500*time.Millisecond))
	ctx := context.Background()
	var wg sync.WaitGroup
	var maxSeen atomic.Int64

	for i := 0; i < 50; i++ {
		wg.Add(1)
		cost := int64(i%3 + 1) // cost 1, 2, or 3
		go func(c int64) {
			defer wg.Done()
			s.DoWithCost(ctx, c, func() error {
				cur := s.InFlight()
				for {
					old := maxSeen.Load()
					if cur <= old || maxSeen.CompareAndSwap(old, cur) {
						break
					}
				}
				time.Sleep(time.Millisecond)
				return nil
			})
		}(cost)
	}
	wg.Wait()

	if maxSeen.Load() > 10 {
		t.Fatalf("max weighted = %d, exceeds capacity 10", maxSeen.Load())
	}
}
