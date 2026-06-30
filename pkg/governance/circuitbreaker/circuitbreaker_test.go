package circuitbreaker_test

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/governance/circuitbreaker"
	"github.com/rushteam/beauty/pkg/service/discover"
)

func newSvc(addr string) *discover.ServiceInfo {
	return &discover.ServiceInfo{ID: addr, Addr: addr, Metadata: map[string]string{}}
}

func TestNoopBreaker_AlwaysAvailable(t *testing.T) {
	b := circuitbreaker.NoopBreaker{}
	if !b.Available(newSvc("a")) {
		t.Fatal("noop should always be available")
	}
	b.Report(newSvc("a"), 0, errors.New("err")) // 不 panic
}

func TestNodeBreaker_ClosedByDefault(t *testing.T) {
	b := circuitbreaker.NewNodeBreaker()
	node := newSvc("a")
	if !b.Available(node) {
		t.Fatal("new node should be available (closed)")
	}
}

func TestNodeBreaker_OpenAfterFailures(t *testing.T) {
	b := circuitbreaker.NewNodeBreaker(circuitbreaker.WithFailureThreshold(3))
	node := newSvc("a")
	// 连续失败 2 次不熔断
	b.Report(node, 0, errors.New("e"))
	b.Report(node, 0, errors.New("e"))
	if !b.Available(node) {
		t.Fatal("should still be available after 2 failures (< threshold 3)")
	}
	// 第 3 次失败熔断
	b.Report(node, 0, errors.New("e"))
	if b.Available(node) {
		t.Fatal("should be open (unavailable) after 3 consecutive failures")
	}
}

func TestNodeBreaker_SuccessResetsFailures(t *testing.T) {
	b := circuitbreaker.NewNodeBreaker(circuitbreaker.WithFailureThreshold(3))
	node := newSvc("a")
	b.Report(node, 0, errors.New("e"))
	b.Report(node, 0, errors.New("e"))
	b.Report(node, 0, nil) // 成功,清零
	b.Report(node, 0, errors.New("e"))
	b.Report(node, 0, errors.New("e"))
	if !b.Available(node) {
		t.Fatal("2 failures after reset should not trip (threshold 3)")
	}
}

func TestNodeBreaker_OpenToHalfOpenAfterTimeout(t *testing.T) {
	b := circuitbreaker.NewNodeBreaker(
		circuitbreaker.WithFailureThreshold(1),
		circuitbreaker.WithTimeout(50*time.Millisecond),
	)
	node := newSvc("a")
	b.Report(node, 0, errors.New("e")) // 1 次失败即熔断
	if b.Available(node) {
		t.Fatal("should be open")
	}
	time.Sleep(60 * time.Millisecond)
	// 冷却后进 HalfOpen,放行 1 个探测
	if !b.Available(node) {
		t.Fatal("should be half-open and allow 1 probe after timeout")
	}
	// 半开态只放行 1 个,第二个拒绝
	if b.Available(node) {
		t.Fatal("second Available in half-open should be rejected (1 probe limit)")
	}
}

func TestNodeBreaker_HalfOpenProbeSuccess_Closes(t *testing.T) {
	b := circuitbreaker.NewNodeBreaker(
		circuitbreaker.WithFailureThreshold(1),
		circuitbreaker.WithTimeout(50*time.Millisecond),
		circuitbreaker.WithSuccessThreshold(1),
	)
	node := newSvc("a")
	b.Report(node, 0, errors.New("e")) // Open
	time.Sleep(60 * time.Millisecond)
	b.Available(node)      // 触发 HalfOpen,放行探测
	b.Report(node, 0, nil) // 探测成功 → Closed
	if !b.Available(node) {
		t.Fatal("should be closed after successful probe")
	}
}

func TestNodeBreaker_HalfOpenProbeFailure_Reopens(t *testing.T) {
	b := circuitbreaker.NewNodeBreaker(
		circuitbreaker.WithFailureThreshold(1),
		circuitbreaker.WithTimeout(50*time.Millisecond),
	)
	node := newSvc("a")
	b.Report(node, 0, errors.New("e")) // Open
	time.Sleep(60 * time.Millisecond)
	b.Available(node)                  // HalfOpen 探测
	b.Report(node, 0, errors.New("e")) // 探测失败 → Open
	if b.Available(node) {
		t.Fatal("should reopen after failed probe")
	}
}

func TestNodeBreaker_PerNodeIsolation(t *testing.T) {
	b := circuitbreaker.NewNodeBreaker(circuitbreaker.WithFailureThreshold(1))
	a := newSvc("a")
	c := newSvc("c")
	b.Report(a, 0, errors.New("e"))
	if b.Available(a) {
		t.Error("a should be open")
	}
	if !b.Available(c) {
		t.Error("c should still be closed (per-node isolation)")
	}
}

func TestNodeBreaker_NilNodeSafe(t *testing.T) {
	b := circuitbreaker.NewNodeBreaker()
	if b.Available(nil) {
		t.Fatal("nil node should not be available")
	}
	b.Report(nil, 0, nil) // 不 panic
}

func TestNodeBreaker_OnStateChange(t *testing.T) {
	var changes atomic.Int64
	b := circuitbreaker.NewNodeBreaker(
		circuitbreaker.WithFailureThreshold(1),
		circuitbreaker.WithOnStateChange(func(addr string, from, to circuitbreaker.State) {
			changes.Add(1)
		}),
	)
	node := newSvc("a")
	b.Report(node, 0, errors.New("e")) // Closed→Open
	if changes.Load() != 1 {
		t.Errorf("want 1 state change, got %d", changes.Load())
	}
}

func TestNodeBreaker_OnStateChange_PanicRecovered(t *testing.T) {
	b := circuitbreaker.NewNodeBreaker(
		circuitbreaker.WithFailureThreshold(1),
		circuitbreaker.WithOnStateChange(func(string, circuitbreaker.State, circuitbreaker.State) {
			panic("boom")
		}),
	)
	node := newSvc("a")
	// 回调 panic 不应影响状态机
	b.Report(node, 0, errors.New("e"))
	if b.Available(node) {
		t.Fatal("should still be open after callback panic")
	}
}

func TestNodeBreaker_Stats(t *testing.T) {
	b := circuitbreaker.NewNodeBreaker(circuitbreaker.WithFailureThreshold(1))
	a := newSvc("a")
	c := newSvc("c")
	b.Report(a, 0, errors.New("e"))
	b.Available(c) // 触发 c 进 nodes map(closed)
	stats := b.Stats()
	if len(stats) != 2 {
		t.Fatalf("want 2 nodes in stats, got %d", len(stats))
	}
	if stats["a"].State != circuitbreaker.StateOpen {
		t.Errorf("a should be open, got %s", stats["a"].State)
	}
	if stats["c"].State != circuitbreaker.StateClosed {
		t.Errorf("c should be closed, got %s", stats["c"].State)
	}
}

func TestNodeBreaker_Reset(t *testing.T) {
	b := circuitbreaker.NewNodeBreaker(circuitbreaker.WithFailureThreshold(1))
	node := newSvc("a")
	b.Report(node, 0, errors.New("e"))
	b.Reset()
	if !b.Available(node) {
		t.Fatal("should be closed after Reset")
	}
}

func TestNodeBreaker_Concurrent(t *testing.T) {
	b := circuitbreaker.NewNodeBreaker(circuitbreaker.WithFailureThreshold(100))
	node := newSvc("a")
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			for range 100 {
				b.Available(node)
				b.Report(node, 0, errors.New("e"))
				b.Stats()
			}
		})
	}
	wg.Wait()
	// 不 panic、不死锁即通过
}

func TestStateString(t *testing.T) {
	cases := []circuitbreaker.State{
		circuitbreaker.StateClosed,
		circuitbreaker.StateOpen,
		circuitbreaker.StateHalfOpen,
	}
	for _, s := range cases {
		if s.String() == "unknown" {
			t.Errorf("state %d should have name", s)
		}
	}
	// 覆盖 fmt 输出
	_ = fmt.Sprintf("%v", circuitbreaker.StateClosed)
}
