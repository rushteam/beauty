//go:build integration

// 集成测试:需要真实 Consul agent。运行前设置 BEAUTY_TEST_CONSUL_ADDR,
// 例如本机 docker: BEAUTY_TEST_CONSUL_ADDR=127.0.0.1:8500 go test -tags=integration ./pkg/infra/consul/...
package consul_test

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"

	beautyconsul "github.com/rushteam/beauty/pkg/infra/consul"
)

func consulAddr(t *testing.T) string {
	a := os.Getenv("BEAUTY_TEST_CONSUL_ADDR")
	if a == "" {
		t.Skip("BEAUTY_TEST_CONSUL_ADDR not set, skipping consul integration test")
	}
	return a
}

// newDLock 每次都用独立的 Consul 客户端连接,模拟不同进程/实例。
func newDLock(t *testing.T, ttl time.Duration) *beautyconsul.DLock {
	client, err := api.NewClient(&api.Config{Address: consulAddr(t)})
	if err != nil {
		t.Fatalf("connect consul: %v", err)
	}
	return beautyconsul.NewDLock(client,
		beautyconsul.WithLockKeyPrefix("beauty-test/"),
		beautyconsul.WithLockSessionTTL(ttl),
	)
}

// TestIntegration_Lock_MutualExclusion_AcrossClients 用两个独立连接竞争同一把锁,
// 验证互斥、TryLock 非阻塞、阻塞 Lock 在释放后才返回。
func TestIntegration_Lock_MutualExclusion_AcrossClients(t *testing.T) {
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
	case <-time.After(500 * time.Millisecond):
	}

	if err := l1.Unlock(ctx); err != nil {
		t.Fatalf("d1 unlock: %v", err)
	}

	select {
	case <-acquired:
	case <-time.After(5 * time.Second):
		t.Fatal("d2 did not acquire lock after d1 released")
	}
}

// TestIntegration_Elector_SingleLeaderAcrossClients 5 个独立连接竞选同一 leader key,
// 验证任意时刻至多一个当选,且能多轮选出。
func TestIntegration_Elector_SingleLeaderAcrossClients(t *testing.T) {
	key := "leader-" + time.Now().Format("150405.000000")
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	var active, maxActive atomic.Int32
	var totalElected atomic.Int64

	var wg sync.WaitGroup
	for range 5 {
		d := newDLock(t, 10*time.Second)
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
				case <-time.After(500 * time.Millisecond):
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

// TestIntegration_Elector_FailoverOnSessionLoss leader 连接断开(模拟崩溃)后,
// 另一候选者应在 session TTL 内接管。
func TestIntegration_Elector_FailoverOnSessionLoss(t *testing.T) {
	key := "failover-" + time.Now().Format("150405.000000")

	// d1 用独立的底层 client,以便中途"崩溃"——这里通过 cancel ctx 让它退出参选。
	client1, err := api.NewClient(&api.Config{Address: consulAddr(t)})
	if err != nil {
		t.Fatalf("connect consul: %v", err)
	}
	d1 := beautyconsul.NewDLock(client1,
		beautyconsul.WithLockKeyPrefix("beauty-test/"),
		beautyconsul.WithLockSessionTTL(10*time.Second),
	)
	ctx1, cancel1 := context.WithCancel(context.Background())
	becameLeader1 := make(chan struct{})
	go d1.Run(ctx1, key, func(leaderCtx context.Context) {
		close(becameLeader1)
		<-leaderCtx.Done()
	})

	select {
	case <-becameLeader1:
	case <-time.After(5 * time.Second):
		t.Fatal("d1 never became leader")
	}

	d2 := newDLock(t, 10*time.Second)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel2()
	becameLeader2 := make(chan struct{})
	go d2.Run(ctx2, key, func(leaderCtx context.Context) {
		close(becameLeader2)
		<-leaderCtx.Done()
	})

	select {
	case <-becameLeader2:
		t.Fatal("d2 should not become leader while d1 holds it")
	case <-time.After(1 * time.Second):
	}

	// 模拟 d1 崩溃:取消其 ctx 让 Run 退出并主动让位(Unlock 释放锁 + 停止续期)。
	cancel1()

	select {
	case <-becameLeader2:
	case <-time.After(15 * time.Second):
		t.Fatal("d2 did not take over leadership after d1 released")
	}
}
