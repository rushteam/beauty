package agent_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rushteam/beauty/contrib/llm/agent"
)

func TestRegistry_StartFinishDone(t *testing.T) {
	reg := agent.NewRegistry()
	ctx, finish, ok := reg.Start(context.Background(), "req-1", 0)
	if !ok {
		t.Fatal("Start 应成功")
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("刚 Start 的 ctx 不应已取消: %v", err)
	}
	info, ok := reg.Status("req-1")
	if !ok || info.Status != agent.StatusRunning {
		t.Fatalf("status = %+v, ok=%v, want running", info, ok)
	}
	finish(nil)
	info, ok = reg.Status("req-1")
	if !ok || info.Status != agent.StatusDone {
		t.Fatalf("status = %+v, ok=%v, want done", info, ok)
	}
}

func TestRegistry_DuplicateIDWhileRunning(t *testing.T) {
	reg := agent.NewRegistry()
	_, finish, ok := reg.Start(context.Background(), "req-1", 0)
	if !ok {
		t.Fatal("第一次 Start 应成功")
	}
	if _, _, ok := reg.Start(context.Background(), "req-1", 0); ok {
		t.Fatal("运行中的 id 重复 Start 应失败")
	}
	finish(nil)
	if _, finish2, ok := reg.Start(context.Background(), "req-1", 0); !ok {
		t.Fatal("已结束的 id 应可重新 Start")
	} else {
		finish2(nil)
	}
}

func TestRegistry_Cancel(t *testing.T) {
	reg := agent.NewRegistry()
	ctx, finish, ok := reg.Start(context.Background(), "req-1", 0)
	if !ok {
		t.Fatal("Start 应成功")
	}
	if !reg.Cancel("req-1") {
		t.Fatal("Cancel 应成功")
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("ctx 应在 Cancel 后立即结束")
	}
	finish(ctx.Err())
	info, ok := reg.Status("req-1")
	if !ok || info.Status != agent.StatusCancelled {
		t.Fatalf("status = %+v, ok=%v, want cancelled", info, ok)
	}
	if !errors.Is(info.Err, context.Canceled) {
		t.Fatalf("info.Err = %v, want context.Canceled", info.Err)
	}
}

func TestRegistry_CancelUnknownOrFinished(t *testing.T) {
	reg := agent.NewRegistry()
	if reg.Cancel("nope") {
		t.Fatal("未知 id 的 Cancel 应返回 false")
	}
	_, finish, _ := reg.Start(context.Background(), "req-1", 0)
	finish(nil)
	if reg.Cancel("req-1") {
		t.Fatal("已结束的运行 Cancel 应返回 false")
	}
}

func TestRegistry_Timeout(t *testing.T) {
	reg := agent.NewRegistry()
	ctx, finish, ok := reg.Start(context.Background(), "req-1", 10*time.Millisecond)
	if !ok {
		t.Fatal("Start 应成功")
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("ctx 应在超时后结束")
	}
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("ctx.Err() = %v, want DeadlineExceeded", ctx.Err())
	}
	finish(ctx.Err())
	info, _ := reg.Status("req-1")
	if info.Status != agent.StatusError {
		t.Fatalf("status = %v, want error (DeadlineExceeded 不是 context.Canceled)", info.Status)
	}
}

func TestRegistry_Forget(t *testing.T) {
	reg := agent.NewRegistry()
	_, finish, _ := reg.Start(context.Background(), "req-1", 0)

	reg.Forget("req-1") // 运行中,应被忽略
	if _, ok := reg.Status("req-1"); !ok {
		t.Fatal("运行中的记录不应被 Forget 删除")
	}

	finish(nil)
	reg.Forget("req-1")
	if _, ok := reg.Status("req-1"); ok {
		t.Fatal("已结束的记录应被 Forget 删除")
	}
}
