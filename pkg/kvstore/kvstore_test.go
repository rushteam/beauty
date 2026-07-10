package kvstore_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/kvstore"
)

func TestMemory_IncrAndGetInt(t *testing.T) {
	m := kvstore.NewMemory()
	defer m.Stop()
	ctx := context.Background()

	if _, ok, _ := m.GetInt(ctx, "k"); ok {
		t.Fatal("missing key should be (0,false)")
	}
	v, _ := m.Incr(ctx, "k", 3, time.Minute)
	if v != 3 {
		t.Fatalf("incr = %d, want 3", v)
	}
	v, _ = m.Incr(ctx, "k", 2, time.Minute)
	if v != 5 {
		t.Fatalf("incr = %d, want 5", v)
	}
	if got, ok, _ := m.GetInt(ctx, "k"); !ok || got != 5 {
		t.Fatalf("getint = %d,%v", got, ok)
	}
}

func TestMemory_SetNX(t *testing.T) {
	m := kvstore.NewMemory()
	defer m.Stop()
	ctx := context.Background()

	ok, _ := m.SetNX(ctx, "k", []byte("first"), time.Minute)
	if !ok {
		t.Fatal("first SetNX should win")
	}
	ok, _ = m.SetNX(ctx, "k", []byte("second"), time.Minute)
	if ok {
		t.Fatal("second SetNX should fail (key exists)")
	}
	if b, _, _ := m.Get(ctx, "k"); string(b) != "first" {
		t.Fatalf("value = %q, want first", b)
	}
}

func TestMemory_SetGetDelete(t *testing.T) {
	m := kvstore.NewMemory()
	defer m.Stop()
	ctx := context.Background()

	m.Set(ctx, "k", []byte("v"), time.Minute)
	if b, ok, _ := m.Get(ctx, "k"); !ok || string(b) != "v" {
		t.Fatalf("get = %q,%v", b, ok)
	}
	m.Delete(ctx, "k")
	if _, ok, _ := m.Get(ctx, "k"); ok {
		t.Fatal("deleted key should be gone")
	}
}

func TestMemory_TTLExpiry(t *testing.T) {
	m := kvstore.NewMemory()
	defer m.Stop()
	ctx := context.Background()

	m.Set(ctx, "k", []byte("v"), 30*time.Millisecond)
	if _, ok, _ := m.TTL(ctx, "k"); !ok {
		t.Fatal("TTL should exist right after set")
	}
	time.Sleep(50 * time.Millisecond)
	if _, ok, _ := m.Get(ctx, "k"); ok {
		t.Fatal("key should have expired")
	}
	if _, ok, _ := m.TTL(ctx, "k"); ok {
		t.Fatal("expired key TTL should be false")
	}
}

func TestMemory_IncrTTLNotRefreshed(t *testing.T) {
	m := kvstore.NewMemory()
	defer m.Stop()
	ctx := context.Background()
	// 首次 Incr 设 ttl,第二次不刷新;过期后归零重来。
	m.Incr(ctx, "k", 1, 30*time.Millisecond)
	m.Incr(ctx, "k", 1, time.Hour) // ttl 不应被刷新为 1h
	time.Sleep(50 * time.Millisecond)
	if v, ok, _ := m.GetInt(ctx, "k"); ok {
		t.Fatalf("should have expired despite second ttl, got %d", v)
	}
}

func TestMemory_NoExpiry(t *testing.T) {
	m := kvstore.NewMemory()
	defer m.Stop()
	ctx := context.Background()
	m.Set(ctx, "k", []byte("v"), 0) // 永不过期
	d, ok, _ := m.TTL(ctx, "k")
	if !ok || d < time.Hour {
		t.Fatalf("no-expiry TTL = %v,%v", d, ok)
	}
}

func TestMemory_Concurrent(t *testing.T) {
	m := kvstore.NewMemory()
	defer m.Stop()
	ctx := context.Background()
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			for range 1000 {
				m.Incr(ctx, "hot", 1, time.Minute)
			}
		})
	}
	wg.Wait()
	if v, _, _ := m.GetInt(ctx, "hot"); v != 50000 {
		t.Fatalf("concurrent incr = %d, want 50000", v)
	}
}
