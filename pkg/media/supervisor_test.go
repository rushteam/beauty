package media_test

import (
	"context"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/backoff"
	"github.com/rushteam/beauty/pkg/media"
)

func TestSupervisor_RestartsOnExit(t *testing.T) {
	var starts atomic.Int32
	sup := media.NewSupervisor(func() *exec.Cmd {
		starts.Add(1)
		return exec.Command("true") // 立刻退出 → 触发重启
	}, media.WithRestartPolicy(backoff.New(backoff.WithBase(10*time.Millisecond), backoff.WithMax(20*time.Millisecond))))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()

	time.Sleep(200 * time.Millisecond) // 期间应重启多次
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run 未在 ctx 取消后返回")
	}
	if n := starts.Load(); n < 2 {
		t.Fatalf("进程应被重启多次, starts=%d", n)
	}
}

func TestSupervisor_GracefulStop(t *testing.T) {
	sup := media.NewSupervisor(func() *exec.Cmd {
		return exec.Command("sleep", "60") // 长跑进程
	}, media.WithStopGrace(time.Second))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()

	time.Sleep(150 * time.Millisecond) // 确保已启动
	start := time.Now()
	cancel()
	select {
	case <-done:
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("优雅停止太慢: %v", elapsed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ctx 取消后应尽快停止长跑进程")
	}
}
