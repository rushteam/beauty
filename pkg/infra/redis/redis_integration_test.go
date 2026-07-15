//go:build integration

// 集成测试:需要真实 Redis。运行前设置 BEAUTY_TEST_REDIS_ADDR,例如本机 docker:
// BEAUTY_TEST_REDIS_ADDR=127.0.0.1:6379 go test -tags=integration ./pkg/infra/redis/...
package redis_test

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"

	beautyredis "github.com/rushteam/beauty/pkg/infra/redis"
)

func redisAddr(t *testing.T) string {
	a := os.Getenv("BEAUTY_TEST_REDIS_ADDR")
	if a == "" {
		t.Skip("BEAUTY_TEST_REDIS_ADDR not set, skipping redis integration test")
	}
	return a
}

func newRawClient(t *testing.T) *goredis.Client {
	c := beautyredis.NewClient(&beautyredis.Config{Addr: redisAddr(t)})
	t.Cleanup(func() { c.Close() })
	return c
}

func newDLock(t *testing.T, ttl time.Duration) *beautyredis.DLock {
	return beautyredis.NewDLock(newRawClient(t),
		beautyredis.WithKeyPrefix("beauty-test:dlock:"),
		beautyredis.WithTTL(ttl),
		beautyredis.WithRetryInterval(50*time.Millisecond),
	)
}

func TestIntegration_Lock_MutualExclusion(t *testing.T) {
	key := "lock-" + time.Now().Format("150405.000000")
	d1 := newDLock(t, 10*time.Second)
	d2 := newDLock(t, 10*time.Second)
	ctx := context.Background()

	l1, err := d1.Lock(ctx, key)
	if err != nil {
		t.Fatalf("d1 lock: %v", err)
	}
	if _, ok, err := d2.TryLock(ctx, key); err != nil || ok {
		t.Fatalf("d2 should NOT acquire while d1 holds: ok=%v err=%v", ok, err)
	}

	acquired := make(chan struct{})
	go func() {
		l2, err := d2.Lock(ctx, key)
		if err != nil {
			t.Errorf("d2 blocking lock: %v", err)
			return
		}
		close(acquired)
		l2.Unlock(ctx)
	}()

	select {
	case <-acquired:
		t.Fatal("d2 acquired before d1 released")
	case <-time.After(300 * time.Millisecond):
	}
	if err := l1.Unlock(ctx); err != nil {
		t.Fatalf("d1 unlock: %v", err)
	}
	select {
	case <-acquired:
	case <-time.After(5 * time.Second):
		t.Fatal("d2 did not acquire after d1 released")
	}
}

func TestIntegration_Unlock_OnlyReleasesOwnToken(t *testing.T) {
	key := "own-" + time.Now().Format("150405.000000")
	d := newDLock(t, 10*time.Second)
	ctx := context.Background()

	l1, err := d.Lock(ctx, key)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	// 释放后另一持有者拿到锁;再对旧句柄 Unlock 不应误删新持有者的锁。
	if err := l1.Unlock(ctx); err != nil {
		t.Fatalf("unlock l1: %v", err)
	}
	l2, ok, err := d.TryLock(ctx, key)
	if err != nil || !ok {
		t.Fatalf("l2 should acquire after release: ok=%v err=%v", ok, err)
	}
	// 旧句柄再次 Unlock(幂等,且 CAS 保证不误删 l2)。
	if err := l1.Unlock(ctx); err != nil {
		t.Fatalf("stale unlock l1: %v", err)
	}
	if _, ok, _ := d.TryLock(ctx, key); ok {
		t.Fatal("stale l1.Unlock wrongly released l2's lock")
	}
	l2.Unlock(ctx)
}

func TestIntegration_Elector_SingleLeader(t *testing.T) {
	key := "leader-" + time.Now().Format("150405.000000")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var active, maxActive atomic.Int32
	var totalElected atomic.Int64
	var wg sync.WaitGroup
	for range 5 {
		d := newDLock(t, 3*time.Second)
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.Run(ctx, key, func(leaderCtx context.Context) {
				n := active.Add(1)
				for {
					old := maxActive.Load()
					if n <= old || maxActive.CompareAndSwap(old, n) {
						break
					}
				}
				totalElected.Add(1)
				select {
				case <-time.After(400 * time.Millisecond):
				case <-leaderCtx.Done():
				}
				active.Add(-1)
			})
		}()
	}
	wg.Wait()

	if maxActive.Load() != 1 {
		t.Fatalf("max concurrent leaders = %d, want 1", maxActive.Load())
	}
	if totalElected.Load() < 2 {
		t.Fatalf("expected >=2 election rounds, got %d", totalElected.Load())
	}
	t.Logf("total election rounds across 5 clients: %d", totalElected.Load())
}

func TestIntegration_Store(t *testing.T) {
	s := beautyredis.NewStore(newRawClient(t), beautyredis.WithStoreKeyPrefix("beauty-test:kv:"))
	ctx := context.Background()
	key := "k-" + time.Now().Format("150405.000000")

	// Incr + 首次设 TTL
	if v, err := s.Incr(ctx, key, 3, 5*time.Second); err != nil || v != 3 {
		t.Fatalf("incr#1 = %d, %v; want 3", v, err)
	}
	if v, err := s.Incr(ctx, key, 2, 5*time.Second); err != nil || v != 5 {
		t.Fatalf("incr#2 = %d, %v; want 5", v, err)
	}
	if got, ok, err := s.GetInt(ctx, key); err != nil || !ok || got != 5 {
		t.Fatalf("getint = %d,%v,%v; want 5,true,nil", got, ok, err)
	}
	if d, ok, err := s.TTL(ctx, key); err != nil || !ok || d <= 0 || d > 5*time.Second {
		t.Fatalf("ttl = %v,%v,%v; want (0,5s],true,nil", d, ok, err)
	}

	// SetNX 抢占
	sk := key + "-nx"
	if ok, err := s.SetNX(ctx, sk, []byte("a"), time.Minute); err != nil || !ok {
		t.Fatalf("setnx#1 = %v,%v; want true,nil", ok, err)
	}
	if ok, err := s.SetNX(ctx, sk, []byte("b"), time.Minute); err != nil || ok {
		t.Fatalf("setnx#2 = %v,%v; want false,nil", ok, err)
	}
	if b, ok, err := s.Get(ctx, sk); err != nil || !ok || string(b) != "a" {
		t.Fatalf("get = %q,%v,%v; want a,true,nil", b, ok, err)
	}

	// 不存在的 key
	if _, ok, err := s.GetInt(ctx, "missing-"+key); err != nil || ok {
		t.Fatalf("getint missing = ok=%v err=%v; want false,nil", ok, err)
	}
	if _, ok, err := s.TTL(ctx, "missing-"+key); err != nil || ok {
		t.Fatalf("ttl missing = ok=%v err=%v; want false,nil", ok, err)
	}

	// Delete
	if err := s.Delete(ctx, key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, _ := s.GetInt(ctx, key); ok {
		t.Fatal("key should be gone after delete")
	}
	s.Delete(ctx, sk)
}
