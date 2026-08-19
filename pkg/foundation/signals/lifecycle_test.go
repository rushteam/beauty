package signals_test

import (
	"context"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/foundation/signals"
)

func TestDetachTimeout_ParentCancel(t *testing.T) {
	parent, parentCancel := context.WithCancel(context.Background())
	parentCancel()

	ctx, cancel := signals.DetachTimeout(parent, 2*time.Second)
	defer cancel()

	select {
	case <-ctx.Done():
		t.Fatal("detached ctx should not inherit parent cancel")
	default:
	}

	// 能在 parent 已取消后完成短操作
	done := make(chan struct{})
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("detached ctx expired before short I/O completed")
	}
}

func TestDetachTimeout_Timeout(t *testing.T) {
	ctx, cancel := signals.DetachTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	select {
	case <-ctx.Done():
	case <-time.After(1 * time.Second):
		t.Fatal("detached ctx should have timed out")
	}
}

func TestDetachTimeout_PreservesValues(t *testing.T) {
	type ctxKey struct{}
	parent := context.WithValue(context.Background(), ctxKey{}, "traceID-123")

	ctx, cancel := signals.DetachTimeout(parent, time.Second)
	defer cancel()

	if got, ok := ctx.Value(ctxKey{}).(string); !ok || got != "traceID-123" {
		t.Fatalf("value not preserved: %q", got)
	}
}

func TestDetachTimeout_NilParent(t *testing.T) {
	ctx, cancel := signals.DetachTimeout(nil, time.Second)
	defer cancel()

	select {
	case <-ctx.Done():
		t.Fatal("should not be done immediately")
	default:
	}
}

func TestDetachTimeout_ZeroTimeout(t *testing.T) {
	ctx, cancel := signals.DetachTimeout(context.Background(), 0)
	defer cancel()

	// timeout <= 0 应使用默认值 5s，不应立即过期
	select {
	case <-ctx.Done():
		t.Fatal("zero timeout should use default 5s, not expire immediately")
	default:
	}
}
