package dlock_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/dlock"
)

func TestLock_MutualExclusion(t *testing.T) {
	m := dlock.NewMemory()
	ctx := context.Background()

	l1, err := m.Lock(ctx, "k")
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	// 第二个 Lock 应阻塞;用 TryLock 验证确实拿不到。
	if _, ok, _ := m.TryLock(ctx, "k"); ok {
		t.Fatal("TryLock should fail while held")
	}
	if err := l1.Unlock(ctx); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	// 释放后可再获取。
	l2, ok, _ := m.TryLock(ctx, "k")
	if !ok {
		t.Fatal("TryLock after unlock should succeed")
	}
	l2.Unlock(ctx)
}

func TestUnlock_Idempotent(t *testing.T) {
	m := dlock.NewMemory()
	l, _ := m.Lock(context.Background(), "k")
	l.Unlock(context.Background())
	if err := l.Unlock(context.Background()); err != nil {
		t.Fatalf("double unlock should be no-op, got %v", err)
	}
}

func TestLock_DifferentKeysIndependent(t *testing.T) {
	m := dlock.NewMemory()
	ctx := context.Background()
	l1, _ := m.Lock(ctx, "a")
	l2, ok, _ := m.TryLock(ctx, "b")
	if !ok {
		t.Fatal("different key should not be blocked")
	}
	l1.Unlock(ctx)
	l2.Unlock(ctx)
}

func TestLock_ContextCancel(t *testing.T) {
	m := dlock.NewMemory()
	held, _ := m.Lock(context.Background(), "k")
	defer held.Unlock(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := m.Lock(ctx, "k")
	if err != context.DeadlineExceeded {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
}

func TestElector_MutualExclusion(t *testing.T) {
	m := dlock.NewMemory()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	var active atomic.Int32
	var maxActive atomic.Int32
	var rounds atomic.Int64

	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			m.Run(ctx, "leader", func(leaderCtx context.Context) {
				n := active.Add(1)
				for {
					old := maxActive.Load()
					if n <= old || maxActive.CompareAndSwap(old, n) {
						break
					}
				}
				rounds.Add(1)
				time.Sleep(5 * time.Millisecond) // 模拟当选期间的工作
				active.Add(-1)
			})
		})
	}
	wg.Wait()

	if maxActive.Load() != 1 {
		t.Fatalf("max concurrent leaders = %d, want 1", maxActive.Load())
	}
	if rounds.Load() < 2 {
		t.Fatalf("expected multiple election rounds within 300ms, got %d", rounds.Load())
	}
}

func TestElector_LeaderCtxCancelledAfterOnElectedReturns(t *testing.T) {
	m := dlock.NewMemory()
	ctx, cancel := context.WithCancel(context.Background())

	var captured context.Context
	var mu sync.Mutex
	rounds := 0

	go m.Run(ctx, "k", func(leaderCtx context.Context) {
		mu.Lock()
		captured = leaderCtx
		rounds++
		if rounds == 1 {
			cancel() // 停在第一轮之后,不再重新参选
		}
		mu.Unlock()
	})

	time.Sleep(30 * time.Millisecond) // 给第一轮跑完 + Run 观察到 ctx 取消退出的时间

	mu.Lock()
	defer mu.Unlock()
	if captured == nil {
		t.Fatal("onElected should have been called at least once")
	}
	select {
	case <-captured.Done():
	default:
		t.Fatal("leaderCtx should be cancelled once onElected has returned")
	}
}

func TestElector_StopsOnContextCancel(t *testing.T) {
	m := dlock.NewMemory()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- m.Run(ctx, "k", func(leaderCtx context.Context) {
			<-leaderCtx.Done() // 长期持有,直到失去 leader
		})
	}()
	time.Sleep(10 * time.Millisecond) // 确保已当选
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("err = %v, want Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not stop after ctx cancel")
	}
}
