package callbacks

import (
	"context"
	"errors"
	"testing"
)

type startKey struct{}

func TestLocalHandler_LifecycleAndCtxThreading(t *testing.T) {
	var started, ended bool
	h := NewHandlerBuilder().
		OnStart(func(ctx context.Context, _ *RunInfo, _ any) context.Context {
			started = true
			return context.WithValue(ctx, startKey{}, "v")
		}).
		OnEnd(func(ctx context.Context, _ *RunInfo, _ any) context.Context {
			ended = true
			// OnStart 写入的值应在同一 ctx 链可见
			if ctx.Value(startKey{}) != "v" {
				t.Error("ctx from OnStart not threaded to OnEnd")
			}
			return ctx
		}).
		Build()

	ctx := WithHandlers(context.Background(), h)
	info := &RunInfo{Name: "Do"}
	ctx = OnStart(ctx, info, "in")
	OnEnd(ctx, info, "out")

	if !started || !ended {
		t.Fatalf("handlers not invoked: start=%v end=%v", started, ended)
	}
}

func TestTimingChecker_SkipsUnsetTimings(t *testing.T) {
	calls := 0
	// 只设置 OnError，OnStart/OnEnd 应被 TimingChecker 跳过
	h := NewHandlerBuilder().
		OnError(func(ctx context.Context, _ *RunInfo, _ error) context.Context {
			calls++
			return ctx
		}).
		Build()

	if h.(TimingChecker).Needed(context.Background(), nil, TimingStart) {
		t.Error("OnStart timing should not be needed")
	}
	if !h.(TimingChecker).Needed(context.Background(), nil, TimingError) {
		t.Error("OnError timing should be needed")
	}

	ctx := WithHandlers(context.Background(), h)
	info := &RunInfo{}
	OnStart(ctx, info, nil) // 应被跳过
	OnEnd(ctx, info, nil)   // 应被跳过
	OnError(ctx, info, errors.New("x"))
	if calls != 1 {
		t.Fatalf("only OnError should fire, got %d calls", calls)
	}
}

func TestOnError(t *testing.T) {
	var gotErr error
	h := NewHandlerBuilder().
		OnError(func(ctx context.Context, _ *RunInfo, err error) context.Context {
			gotErr = err
			return ctx
		}).Build()
	ctx := WithHandlers(context.Background(), h)
	sentinel := errors.New("boom")
	OnError(ctx, &RunInfo{}, sentinel)
	if !errors.Is(gotErr, sentinel) {
		t.Fatalf("want sentinel, got %v", gotErr)
	}
}

func TestMultipleHandlers_InOrder(t *testing.T) {
	var order []string
	h1 := NewHandlerBuilder().OnStart(func(ctx context.Context, _ *RunInfo, _ any) context.Context {
		order = append(order, "h1")
		return ctx
	}).Build()
	h2 := NewHandlerBuilder().OnStart(func(ctx context.Context, _ *RunInfo, _ any) context.Context {
		order = append(order, "h2")
		return ctx
	}).Build()

	ctx := WithHandlers(context.Background(), h1, h2)
	OnStart(ctx, &RunInfo{}, nil)
	if len(order) != 2 || order[0] != "h1" || order[1] != "h2" {
		t.Fatalf("handlers should run in registration order: %v", order)
	}
}

func TestNoHandlers_NoOp(t *testing.T) {
	// 无 handler 时各调用应安全返回原 ctx
	ctx := context.Background()
	if OnStart(ctx, &RunInfo{}, nil) != ctx {
		t.Error("OnStart with no handlers should return same ctx")
	}
}

func TestWithHandlers_Merges(t *testing.T) {
	n := 0
	mk := func() Handler {
		return NewHandlerBuilder().OnStart(func(ctx context.Context, _ *RunInfo, _ any) context.Context {
			n++
			return ctx
		}).Build()
	}
	ctx := WithHandlers(context.Background(), mk())
	ctx = WithHandlers(ctx, mk()) // 第二次附加应与第一次合并
	OnStart(ctx, &RunInfo{}, nil)
	if n != 2 {
		t.Fatalf("both merged handlers should fire, got %d", n)
	}
}
