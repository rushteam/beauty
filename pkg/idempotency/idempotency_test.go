package idempotency_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/idempotency"
)

func TestDo_RepeatKeyReusesResult(t *testing.T) {
	s := idempotency.New[int]()
	defer s.Stop()

	var calls atomic.Int64
	fn := func() (int, error) { calls.Add(1); return 42, nil }

	r1, err1, shared1 := s.Do("k", fn)
	if r1 != 42 || err1 != nil || shared1 {
		t.Fatalf("first: got (%d,%v,%v)", r1, err1, shared1)
	}
	r2, err2, shared2 := s.Do("k", fn)
	if r2 != 42 || err2 != nil || !shared2 {
		t.Fatalf("second: got (%d,%v,%v), want (42,nil,true)", r2, err2, shared2)
	}
	if calls.Load() != 1 {
		t.Fatalf("fn should run once, ran %d times", calls.Load())
	}
}

func TestDo_ConcurrentSameKeySingleflight(t *testing.T) {
	s := idempotency.New[int]()
	defer s.Stop()

	var calls atomic.Int64
	start := make(chan struct{})
	fn := func() (int, error) {
		calls.Add(1)
		time.Sleep(20 * time.Millisecond) // 让并发请求都挤进 "执行中"
		return 7, nil
	}

	const n = 50
	var wg sync.WaitGroup
	var sharedCount atomic.Int64
	for range n {
		wg.Go(func() {
			<-start
			r, err, shared := s.Do("k", fn)
			if r != 7 || err != nil {
				t.Errorf("got (%d,%v)", r, err)
			}
			if shared {
				sharedCount.Add(1)
			}
		})
	}
	close(start)
	wg.Wait()

	if calls.Load() != 1 {
		t.Fatalf("fn should run exactly once under concurrency, ran %d", calls.Load())
	}
	if sharedCount.Load() != n-1 {
		t.Fatalf("want %d shared, got %d", n-1, sharedCount.Load())
	}
}

func TestDo_ErrorNotCachedByDefault(t *testing.T) {
	s := idempotency.New[int]()
	defer s.Stop()

	var calls atomic.Int64
	fn := func() (int, error) { calls.Add(1); return 0, errors.New("boom") }

	_, err1, _ := s.Do("k", fn)
	_, err2, shared := s.Do("k", fn)
	if err1 == nil || err2 == nil {
		t.Fatal("both should error")
	}
	if shared {
		t.Fatal("error should not be cached/shared by default")
	}
	if calls.Load() != 2 {
		t.Fatalf("fn should re-run after error, ran %d", calls.Load())
	}
}

func TestDo_ErrorCachedWhenEnabled(t *testing.T) {
	s := idempotency.New[int](idempotency.WithCacheErrors(true))
	defer s.Stop()

	var calls atomic.Int64
	fn := func() (int, error) { calls.Add(1); return 0, errors.New("boom") }

	s.Do("k", fn)
	_, _, shared := s.Do("k", fn)
	if !shared {
		t.Fatal("error should be cached when WithCacheErrors(true)")
	}
	if calls.Load() != 1 {
		t.Fatalf("fn should run once, ran %d", calls.Load())
	}
}

func TestDo_TTLExpiryReExecutes(t *testing.T) {
	s := idempotency.New[int](idempotency.WithTTL(20*time.Millisecond), idempotency.WithGCInterval(5*time.Millisecond))
	defer s.Stop()

	var calls atomic.Int64
	fn := func() (int, error) { calls.Add(1); return int(calls.Load()), nil }

	r1, _, _ := s.Do("k", fn)
	time.Sleep(40 * time.Millisecond)
	r2, _, shared := s.Do("k", fn)
	if r1 != 1 || r2 != 2 {
		t.Fatalf("want re-exec after TTL: r1=%d r2=%d", r1, r2)
	}
	if shared {
		t.Fatal("after expiry should not be shared")
	}
}

func TestGet_And_Forget(t *testing.T) {
	s := idempotency.New[string]()
	defer s.Stop()

	if _, ok := s.Get("k"); ok {
		t.Fatal("empty key should not be found")
	}
	s.Do("k", func() (string, error) { return "v", nil })
	if v, ok := s.Get("k"); !ok || v != "v" {
		t.Fatalf("Get after Do: (%q,%v)", v, ok)
	}
	s.Forget("k")
	if _, ok := s.Get("k"); ok {
		t.Fatal("after Forget should be gone")
	}
}

func TestDo_PanicAllowsRetry(t *testing.T) {
	s := idempotency.New[int]()
	defer s.Stop()

	func() {
		defer func() { _ = recover() }()
		s.Do("k", func() (int, error) { panic("boom") })
	}()
	// panic 后记录应被清理,可重试
	if s.Len() != 0 {
		t.Fatalf("panic should clear the entry, Len=%d", s.Len())
	}
	r, err, _ := s.Do("k", func() (int, error) { return 5, nil })
	if r != 5 || err != nil {
		t.Fatalf("retry after panic: (%d,%v)", r, err)
	}
}
