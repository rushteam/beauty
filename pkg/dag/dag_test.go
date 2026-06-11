package dag

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 记录节点执行顺序与并发情况的辅助工具。
type recorder struct {
	mu    sync.Mutex
	order []string
}

func (r *recorder) node(name string, deps ...string) Node {
	return Node{Name: name, DependsOn: deps, Run: func(_ context.Context) error {
		r.mu.Lock()
		r.order = append(r.order, name)
		r.mu.Unlock()
		return nil
	}}
}

func (r *recorder) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.order...)
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

// 依赖顺序：a -> b -> c，d 依赖 a。验证拓扑顺序被遵守。
func TestRun_RespectsDependencyOrder(t *testing.T) {
	r := &recorder{}
	d := New().Add(
		r.node("b", "a"),
		r.node("c", "b"),
		r.node("a"),
		r.node("d", "a"),
	)
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := r.seen()
	if len(got) != 4 {
		t.Fatalf("want 4 nodes executed, got %v", got)
	}
	// a 在 b/d 之前；b 在 c 之前
	if !(indexOf(got, "a") < indexOf(got, "b")) {
		t.Errorf("a must run before b: %v", got)
	}
	if !(indexOf(got, "a") < indexOf(got, "d")) {
		t.Errorf("a must run before d: %v", got)
	}
	if !(indexOf(got, "b") < indexOf(got, "c")) {
		t.Errorf("b must run before c: %v", got)
	}
}

// 同层节点应并行执行。
func TestRun_LayerRunsConcurrently(t *testing.T) {
	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32
	work := func(_ context.Context) error {
		n := concurrent.Add(1)
		for {
			old := maxConcurrent.Load()
			if n <= old || maxConcurrent.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		concurrent.Add(-1)
		return nil
	}
	d := New().Add(
		Node{Name: "x", Run: work},
		Node{Name: "y", Run: work},
		Node{Name: "z", Run: work},
	)
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if maxConcurrent.Load() < 2 {
		t.Fatalf("expected concurrent execution within a layer, max=%d", maxConcurrent.Load())
	}
}

func TestRun_FailFastStopsLaterLayers(t *testing.T) {
	var cRan atomic.Bool
	boom := errors.New("boom")
	d := New().Add(
		Node{Name: "a", Run: func(_ context.Context) error { return boom }},
		Node{Name: "c", DependsOn: []string{"a"}, Run: func(_ context.Context) error {
			cRan.Store(true)
			return nil
		}},
	)
	err := d.Run(context.Background())
	if err == nil || !errors.Is(err, boom) {
		t.Fatalf("want boom error, got %v", err)
	}
	if cRan.Load() {
		t.Error("downstream node c must NOT run after dependency failed (fail-fast)")
	}
}

func TestRun_ContinueOnErrorAggregates(t *testing.T) {
	e1 := errors.New("e1")
	e2 := errors.New("e2")
	d := New(WithStrategy(ContinueOnError)).Add(
		Node{Name: "a", Run: func(_ context.Context) error { return e1 }},
		Node{Name: "b", Run: func(_ context.Context) error { return e2 }},
	)
	err := d.Run(context.Background())
	if err == nil {
		t.Fatal("want aggregated error")
	}
	if !errors.Is(err, e1) || !errors.Is(err, e2) {
		t.Fatalf("aggregated error must contain both e1 and e2: %v", err)
	}
}

func TestValidate_Cycle(t *testing.T) {
	d := New().Add(
		Node{Name: "a", DependsOn: []string{"b"}},
		Node{Name: "b", DependsOn: []string{"a"}},
	)
	if err := d.Validate(); err == nil {
		t.Fatal("want cycle error")
	}
}

func TestValidate_MissingDependency(t *testing.T) {
	d := New().Add(Node{Name: "a", DependsOn: []string{"ghost"}})
	if err := d.Validate(); err == nil {
		t.Fatal("want missing-dependency error")
	}
}

func TestValidate_DuplicateName(t *testing.T) {
	d := New().Add(Node{Name: "a"}, Node{Name: "a"})
	if err := d.Validate(); err == nil {
		t.Fatal("want duplicate-name error")
	}
}

func TestRun_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	d := New().Add(
		Node{Name: "a", Run: func(_ context.Context) error { cancel(); return nil }},
		Node{Name: "b", DependsOn: []string{"a"}, Run: func(_ context.Context) error {
			t.Error("b must not run after ctx canceled")
			return nil
		}},
	)
	if err := d.Run(ctx); err == nil {
		t.Fatal("want cancellation error")
	}
}

func TestRun_NilRunIsNoop(t *testing.T) {
	var bRan atomic.Bool
	d := New().Add(
		Node{Name: "a"}, // 空节点
		Node{Name: "b", DependsOn: []string{"a"}, Run: func(_ context.Context) error {
			bRan.Store(true)
			return nil
		}},
	)
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !bRan.Load() {
		t.Error("b should run after empty node a")
	}
}

func TestRun_Empty(t *testing.T) {
	if err := New().Run(context.Background()); err != nil {
		t.Fatalf("empty DAG should succeed, got %v", err)
	}
}

func ExampleDAG() {
	// build -> [test, lint] -> deploy
	d := New().Add(
		Node{Name: "build", Run: func(ctx context.Context) error { return nil }},
		Node{Name: "test", DependsOn: []string{"build"}, Run: func(ctx context.Context) error { return nil }},
		Node{Name: "lint", DependsOn: []string{"build"}, Run: func(ctx context.Context) error { return nil }},
		Node{Name: "deploy", DependsOn: []string{"test", "lint"}, Run: func(ctx context.Context) error { return nil }},
	)
	if err := d.Run(context.Background()); err != nil {
		panic(err)
	}
}
