package cache_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/cache"
)

// 同 key 并发 miss 经 singleflight 合并,只回源一次。
func TestLoader_SingleflightDedup(t *testing.T) {
	var calls int32
	l := cache.NewLRULoader(100, func(_ context.Context, key string) (int, error) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(20 * time.Millisecond) // 制造并发窗口
		return 42, nil
	})
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if v, err := l.Get(context.Background(), "k"); err != nil || v != 42 {
				t.Errorf("v=%d err=%v", v, err)
			}
		}()
	}
	wg.Wait()
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("并发同 key 应只回源一次, calls=%d", n)
	}
}

func TestLoader_TTLExpiry(t *testing.T) {
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	var calls int32
	l := cache.NewLRULoader(100, func(_ context.Context, key string) (int, error) {
		atomic.AddInt32(&calls, 1)
		return 1, nil
	}, cache.WithTTL(time.Minute), cache.WithClock(clock))

	_, _ = l.Get(context.Background(), "k")
	_, _ = l.Get(context.Background(), "k") // 命中缓存
	if calls != 1 {
		t.Fatalf("TTL 内应只回源一次, calls=%d", calls)
	}
	now = now.Add(2 * time.Minute) // 过期
	_, _ = l.Get(context.Background(), "k")
	if calls != 2 {
		t.Fatalf("过期后应再回源, calls=%d", calls)
	}
}

func TestLoader_NegativeCache(t *testing.T) {
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	errBoom := errors.New("boom")
	var calls int32
	l := cache.NewLRULoader(100, func(_ context.Context, key string) (int, error) {
		atomic.AddInt32(&calls, 1)
		return 0, errBoom
	}, cache.WithNegativeTTL(time.Minute), cache.WithClock(clock))

	if _, err := l.Get(context.Background(), "k"); !errors.Is(err, errBoom) {
		t.Fatalf("应返回错误, got %v", err)
	}
	_, _ = l.Get(context.Background(), "k") // 负缓存命中,不再回源
	if calls != 1 {
		t.Fatalf("负缓存内失败应只回源一次, calls=%d", calls)
	}
}

// 不配 negTTL 时,失败不缓存,每次都回源。
func TestLoader_NoNegativeCacheByDefault(t *testing.T) {
	var calls int32
	l := cache.NewLRULoader(100, func(_ context.Context, key string) (int, error) {
		atomic.AddInt32(&calls, 1)
		return 0, errors.New("x")
	})
	_, _ = l.Get(context.Background(), "k")
	_, _ = l.Get(context.Background(), "k")
	if calls != 2 {
		t.Fatalf("未配负缓存时失败应每次回源, calls=%d", calls)
	}
}

func TestLoader_Forget(t *testing.T) {
	var calls int32
	now := time.Unix(0, 0)
	l := cache.NewLRULoader(100, func(_ context.Context, key string) (int, error) {
		atomic.AddInt32(&calls, 1)
		return 1, nil
	}, cache.WithTTL(time.Hour), cache.WithClock(func() time.Time { return now }))

	_, _ = l.Get(context.Background(), "k")
	l.Forget("k")
	_, _ = l.Get(context.Background(), "k")
	if calls != 2 {
		t.Fatalf("Forget 后应强制回源, calls=%d", calls)
	}
}
