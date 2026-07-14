//go:build integration

// 集成测试:需要真实 etcd。运行前设置 BEAUTY_TEST_ETCD_ENDPOINTS(逗号分隔),
// 例如本机 docker: BEAUTY_TEST_ETCD_ENDPOINTS=localhost:23790 go test -tags=integration ./pkg/infra/etcd/...
package etcd_test

import (
	"context"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"

	beautyetcd "github.com/rushteam/beauty/pkg/infra/etcd"
)

func endpoints(t *testing.T) []string {
	e := os.Getenv("BEAUTY_TEST_ETCD_ENDPOINTS")
	if e == "" {
		t.Skip("BEAUTY_TEST_ETCD_ENDPOINTS not set, skipping etcd integration test")
	}
	return strings.Split(e, ",")
}

// newClient 每次都建立独立的 clientv3.Client 连接,模拟不同进程/实例。
func newClient(t *testing.T) *clientv3.Client {
	c, err := clientv3.New(clientv3.Config{Endpoints: endpoints(t), DialTimeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("connect etcd: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// TestIntegration_Lock_MutualExclusion_AcrossClients 用两个独立的 etcd 客户端连接
// (模拟两个进程)竞争同一把锁,验证互斥、阻塞等待、释放后可再获取——真实跨进程语义。
func TestIntegration_Lock_MutualExclusion_AcrossClients(t *testing.T) {
	key := "test-lock-" + time.Now().Format("150405.000000")

	c1 := newClient(t)
	c2 := newClient(t)
	d1 := beautyetcd.NewDLock(c1, beautyetcd.WithKeyPrefix("/beauty-test/"), beautyetcd.WithSessionTTL(3))
	d2 := beautyetcd.NewDLock(c2, beautyetcd.WithKeyPrefix("/beauty-test/"), beautyetcd.WithSessionTTL(3))

	ctx := context.Background()
	l1, err := d1.Lock(ctx, key)
	if err != nil {
		t.Fatalf("d1 lock: %v", err)
	}

	// d2(另一个"进程")此刻应拿不到锁。
	if _, ok, err := d2.TryLock(ctx, key); err != nil || ok {
		t.Fatalf("d2 should NOT acquire while d1 holds: ok=%v err=%v", ok, err)
	}

	// d2 阻塞式 Lock,应在 d1 释放后才返回。
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
	case <-time.After(200 * time.Millisecond):
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

// TestIntegration_Elector_SingleLeaderAcrossClients 用 5 个独立客户端连接竞选同一个
// leader key,验证任意时刻只有一个当选,且总能选出结果。
func TestIntegration_Elector_SingleLeaderAcrossClients(t *testing.T) {
	key := "test-leader-" + time.Now().Format("150405.000000")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	var active atomic.Int32
	var maxActive atomic.Int32
	var totalElected atomic.Int64

	var wg sync.WaitGroup
	for i := range 5 {
		client := newClient(t)
		d := beautyetcd.NewDLock(client, beautyetcd.WithKeyPrefix("/beauty-test/"), beautyetcd.WithSessionTTL(3))
		wg.Add(1)
		go func(idx int) {
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
				// 持有一小段时间再主动放弃,让其他候选者有机会当选。
				select {
				case <-time.After(500 * time.Millisecond):
				case <-leaderCtx.Done():
				}
				active.Add(-1)
			})
		}(i)
	}
	wg.Wait()

	if maxActive.Load() != 1 {
		t.Fatalf("max concurrent leaders across %d independent etcd clients = %d, want 1",
			5, maxActive.Load())
	}
	if totalElected.Load() < 2 {
		t.Fatalf("expected at least 2 election rounds within 8s, got %d", totalElected.Load())
	}
	t.Logf("total election rounds across 5 clients: %d", totalElected.Load())
}

// TestIntegration_Elector_FailoverOnSessionLoss 验证:leader 所在的客户端连接断开后
// (模拟进程崩溃),另一个客户端能在会话 TTL 内接管 leader 身份。
func TestIntegration_Elector_FailoverOnSessionLoss(t *testing.T) {
	key := "test-failover-" + time.Now().Format("150405.000000")

	c1 := newClient(t)
	d1 := beautyetcd.NewDLock(c1, beautyetcd.WithKeyPrefix("/beauty-test/"), beautyetcd.WithSessionTTL(2))

	ctx1, cancel1 := context.WithCancel(context.Background())
	becameLeader1 := make(chan struct{})
	go d1.Run(ctx1, key, func(leaderCtx context.Context) {
		close(becameLeader1)
		<-leaderCtx.Done() // 一直持有,直到被外部打断
	})

	select {
	case <-becameLeader1:
	case <-time.After(3 * time.Second):
		t.Fatal("d1 never became leader")
	}

	// 第二个候选者此刻应处于等待(未当选)。
	c2 := newClient(t)
	d2 := beautyetcd.NewDLock(c2, beautyetcd.WithKeyPrefix("/beauty-test/"), beautyetcd.WithSessionTTL(2))
	ctx2, cancel2 := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel2()
	becameLeader2 := make(chan struct{})
	go d2.Run(ctx2, key, func(leaderCtx context.Context) {
		close(becameLeader2)
		<-leaderCtx.Done()
	})

	select {
	case <-becameLeader2:
		t.Fatal("d2 should not become leader while d1 holds it")
	case <-time.After(500 * time.Millisecond):
	}

	// 模拟 d1 所在"进程"崩溃:直接关闭连接(不走 Resign/Unlock 的优雅路径)。
	cancel1()
	c1.Close()

	// d2 应在 session TTL(2s)附近接管 leader。
	select {
	case <-becameLeader2:
	case <-time.After(10 * time.Second):
		t.Fatal("d2 did not take over leadership after d1's session was lost")
	}
}
