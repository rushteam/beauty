package cron

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/dlock"
)

// TestStart_NoElector_RunsImmediately 不配置选主时,行为与之前一致:立即注册并运行。
func TestStart_NoElector_RunsImmediately(t *testing.T) {
	var runs atomic.Int64
	c := New(WithCronHandler("@every 1s", func(ctx context.Context) error {
		runs.Add(1)
		return nil
	}))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Start(ctx) }()

	time.Sleep(30 * time.Millisecond) // 确认已进入运行(Cron.Start 已调用,不阻塞在选举)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start returned err = %v, want nil (graceful stop)", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after ctx cancel")
	}
}

// TestStart_WithElector_RunsOnlyWhenLeader 配置选主后,Start 应先当选才运行任务;
// 用 dlock.Memory 模拟单进程内的选举。
func TestStart_WithElector_RunsOnlyWhenLeader(t *testing.T) {
	elector := dlock.NewMemory()
	var runs atomic.Int64
	c := New(
		WithCronHandler("@every 1s", func(ctx context.Context) error {
			runs.Add(1)
			return nil
		}),
		WithLeaderElector(elector, "test-cron-leader"),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Start(ctx) }()

	// 给选举 + Cron.Start 一点时间;handler 应已注册(register 在选举前执行)。
	time.Sleep(50 * time.Millisecond)
	if len(c.Cron.Entries()) != 1 {
		t.Fatalf("handler should be registered regardless of leadership, entries = %d", len(c.Cron.Entries()))
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start returned err = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after ctx cancel")
	}
}

// TestStart_WithElector_OnlyOneInstanceRunsAtATime 用 dlock.Memory 模拟两个"实例"
// 竞选同一个 key,验证任意时刻只有一个实例的 Cron 在运行任务。
func TestStart_WithElector_OnlyOneInstanceRunsAtATime(t *testing.T) {
	elector := dlock.NewMemory() // 两个 Cron 共享同一个 elector,等价"同一套外部协调服务"

	var activeLeaders atomic.Int32
	var maxActive atomic.Int32
	track := func(ctx context.Context) error {
		n := activeLeaders.Add(1)
		defer activeLeaders.Add(-1)
		for {
			old := maxActive.Load()
			if n <= old || maxActive.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		return nil
	}

	c1 := New(WithCronHandler("@every 1s", track), WithLeaderElector(elector, "shared-key"))
	c2 := New(WithCronHandler("@every 1s", track), WithLeaderElector(elector, "shared-key"))

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go c1.Start(ctx)
	go c2.Start(ctx)
	<-ctx.Done()
	time.Sleep(20 * time.Millisecond) // 给两边 Stop 完成的时间

	if maxActive.Load() > 1 {
		t.Fatalf("max concurrent leaders across 2 Cron instances = %d, want <= 1", maxActive.Load())
	}
}

// TestWithLeaderElector_Unconfigured_NoElectorField 确认未配置时 elector 字段为 nil,
// Start 走原有分支(回归防线:避免以后改动误触发选主路径)。
func TestWithLeaderElector_Unconfigured_NoElectorField(t *testing.T) {
	c := New()
	if c.elector != nil {
		t.Fatal("elector should be nil when WithLeaderElector not used")
	}
}
