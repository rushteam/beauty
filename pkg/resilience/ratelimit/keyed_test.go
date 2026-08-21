package ratelimit

import (
	"sync"
	"testing"
	"time"
)

func TestKeyedLimiter_BasicAllow(t *testing.T) {
	kl := NewKeyedLimiter(func(group string) Limiter {
		return NewTokenBucket(2, 1) // 每组:突发 2,补 1/s
	})
	defer kl.Stop()

	// group-A: 前 2 次放行
	ok, _ := kl.Allow("group-A", "user1")
	if !ok {
		t.Error("first request should be allowed")
	}
	ok, _ = kl.Allow("group-A", "user1")
	if !ok {
		t.Error("second request should be allowed")
	}
	// group-A: 第 3 次超限
	ok, retry := kl.Allow("group-A", "user1")
	if ok {
		t.Error("third request should be rejected")
	}
	if retry <= 0 {
		t.Error("retry should be positive")
	}

	// group-B: 独立,不受 group-A 影响
	ok, _ = kl.Allow("group-B", "user1")
	if !ok {
		t.Error("group-B first request should be allowed")
	}
}

func TestKeyedLimiter_AllowGroup(t *testing.T) {
	kl := NewKeyedLimiter(func(group string) Limiter {
		return NewTokenBucket(1, 1)
	})
	defer kl.Stop()

	ok, _ := kl.AllowGroup("api-v1")
	if !ok {
		t.Error("first should pass")
	}
	ok, _ = kl.AllowGroup("api-v1")
	if ok {
		t.Error("second should be rejected")
	}
}

func TestKeyedLimiter_ConcurrentAccess(t *testing.T) {
	kl := NewKeyedLimiter(func(group string) Limiter {
		return NewTokenBucket(1000, 1000)
	})
	defer kl.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(group string) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				kl.Allow(group, "key")
			}
		}("group-" + string(rune('A'+i%10)))
	}
	wg.Wait()
}

func TestKeyedLimiter_GC(t *testing.T) {
	kl := NewKeyedLimiter(
		func(group string) Limiter {
			return NewTokenBucket(10, 10)
		},
		WithKeyedMaxIdle(50*time.Millisecond),
		WithKeyedGcInterval(30*time.Millisecond),
	)
	defer kl.Stop()

	kl.Allow("ephemeral", "k")
	if kl.Groups() != 1 {
		t.Fatalf("expected 1 group, got %d", kl.Groups())
	}

	time.Sleep(150 * time.Millisecond)
	if kl.Groups() != 0 {
		t.Errorf("expected 0 groups after gc, got %d", kl.Groups())
	}
}

func TestKeyedLimiter_Stop(t *testing.T) {
	kl := NewKeyedLimiter(func(group string) Limiter {
		return NewTokenBucket(10, 10)
	})

	kl.Allow("g1", "k")
	kl.Stop()
	kl.Stop() // 幂等

	if kl.Groups() != 0 {
		t.Error("groups should be 0 after stop")
	}
}
