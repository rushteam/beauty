//go:build integration

// 集成测试:需要真实 etcd。复用 dlock_integration_test.go 的 endpoints/newClient。
// BEAUTY_TEST_ETCD_ENDPOINTS=localhost:23790 go test -tags=integration ./pkg/infra/etcd/...
package etcd_test

import (
	"context"
	"sync"
	"testing"
	"time"

	beautyetcd "github.com/rushteam/beauty/pkg/infra/etcd"
)

func TestIntegration_Store(t *testing.T) {
	s := beautyetcd.NewStore(newClient(t), beautyetcd.WithStoreKeyPrefix("beauty-test/kv/"))
	ctx := context.Background()
	key := "k-" + time.Now().Format("150405.000000")
	t.Cleanup(func() { s.Delete(ctx, key) })

	// Incr + 首次设 TTL(etcd lease 最小 1s,用 5s 便于观察剩余)。
	if v, err := s.Incr(ctx, key, 3, 5*time.Second); err != nil || v != 3 {
		t.Fatalf("incr#1 = %d, %v; want 3", v, err)
	}
	// 第二次 Incr 不刷新 TTL(保留原 lease)。
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
	t.Cleanup(func() { s.Delete(ctx, sk) })
	if ok, err := s.SetNX(ctx, sk, []byte("a"), time.Minute); err != nil || !ok {
		t.Fatalf("setnx#1 = %v,%v; want true,nil", ok, err)
	}
	if ok, err := s.SetNX(ctx, sk, []byte("b"), time.Minute); err != nil || ok {
		t.Fatalf("setnx#2 = %v,%v; want false,nil", ok, err)
	}
	if b, ok, err := s.Get(ctx, sk); err != nil || !ok || string(b) != "a" {
		t.Fatalf("get = %q,%v,%v; want a,true,nil", b, ok, err)
	}

	// 永不过期(ttl<=0)
	pk := key + "-perm"
	t.Cleanup(func() { s.Delete(ctx, pk) })
	if err := s.Set(ctx, pk, []byte("x"), 0); err != nil {
		t.Fatalf("set perm: %v", err)
	}
	if d, ok, err := s.TTL(ctx, pk); err != nil || !ok || d < time.Hour {
		t.Fatalf("ttl perm = %v,%v,%v; want large,true,nil", d, ok, err)
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
}

// TestIntegration_Store_ConcurrentIncr 用多个独立连接并发 Incr 同一 key,验证
// CAS 循环下总和精确(无丢更新)。
func TestIntegration_Store_ConcurrentIncr(t *testing.T) {
	ctx := context.Background()
	key := "cinc-" + time.Now().Format("150405.000000")
	const goroutines, perG = 8, 25

	var wg sync.WaitGroup
	for range goroutines {
		s := beautyetcd.NewStore(newClient(t), beautyetcd.WithStoreKeyPrefix("beauty-test/kv/"))
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range perG {
				if _, err := s.Incr(ctx, key, 1, time.Minute); err != nil {
					t.Errorf("incr: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	s := beautyetcd.NewStore(newClient(t), beautyetcd.WithStoreKeyPrefix("beauty-test/kv/"))
	t.Cleanup(func() { s.Delete(ctx, key) })
	if got, ok, err := s.GetInt(ctx, key); err != nil || !ok || got != goroutines*perG {
		t.Fatalf("final = %d,%v,%v; want %d", got, ok, err, goroutines*perG)
	}
}

// TestIntegration_Store_Expiry 验证 lease 到期后 key 真正消失。
func TestIntegration_Store_Expiry(t *testing.T) {
	s := beautyetcd.NewStore(newClient(t), beautyetcd.WithStoreKeyPrefix("beauty-test/kv/"))
	ctx := context.Background()
	key := "exp-" + time.Now().Format("150405.000000")
	t.Cleanup(func() { s.Delete(ctx, key) })

	if err := s.Set(ctx, key, []byte("v"), 1*time.Second); err != nil {
		t.Fatalf("set: %v", err)
	}
	if _, ok, _ := s.Get(ctx, key); !ok {
		t.Fatal("key should exist right after set")
	}
	time.Sleep(3 * time.Second) // 1s lease + etcd 回收延迟
	if _, ok, err := s.Get(ctx, key); err != nil || ok {
		t.Fatalf("key should expire: ok=%v err=%v", ok, err)
	}
}
