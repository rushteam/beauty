package timerqueue

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func startQueue(t *testing.T, opts ...Option) (*Queue, context.CancelFunc) {
	t.Helper()
	q := New(opts...)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- q.Start(ctx) }()
	<-q.Ready()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return q, cancel
}

func TestAddAndFire(t *testing.T) {
	q, _ := startQueue(t, WithResolution(10*time.Millisecond))

	var fired atomic.Bool
	ok := q.Add("t1", 30*time.Millisecond, func() { fired.Store(true) })
	if !ok {
		t.Fatal("Add returned false")
	}

	time.Sleep(120 * time.Millisecond)
	if !fired.Load() {
		t.Error("task did not fire within expected time")
	}
}

func TestFireOrder(t *testing.T) {
	q, _ := startQueue(t, WithResolution(10*time.Millisecond))

	var mu sync.Mutex
	var order []string

	q.Add("a", 60*time.Millisecond, func() { mu.Lock(); order = append(order, "a"); mu.Unlock() })
	q.Add("b", 20*time.Millisecond, func() { mu.Lock(); order = append(order, "b"); mu.Unlock() })
	q.Add("c", 40*time.Millisecond, func() { mu.Lock(); order = append(order, "c"); mu.Unlock() })

	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 3 {
		t.Fatalf("expected 3 fires, got %d: %v", len(order), order)
	}
	if order[0] != "b" || order[1] != "c" || order[2] != "a" {
		t.Errorf("unexpected order: %v, want [b c a]", order)
	}
}

func TestCancel(t *testing.T) {
	q, _ := startQueue(t, WithResolution(10*time.Millisecond))

	var fired atomic.Bool
	q.Add("cancel-me", 50*time.Millisecond, func() { fired.Store(true) })
	q.Cancel("cancel-me")

	time.Sleep(150 * time.Millisecond)
	if fired.Load() {
		t.Error("cancelled task should not fire")
	}
	if q.Pending() != 0 {
		t.Errorf("pending should be 0 after cancel, got %d", q.Pending())
	}
}

func TestCancelEmptyID(t *testing.T) {
	q, _ := startQueue(t, WithResolution(10*time.Millisecond))
	if q.Cancel("") {
		t.Error("Cancel with empty ID should return false")
	}
}

func TestAddNilFn(t *testing.T) {
	q, _ := startQueue(t, WithResolution(10*time.Millisecond))
	if q.Add("nil-fn", time.Second, nil) {
		t.Error("Add with nil fn should return false")
	}
}

func TestReplaceByID(t *testing.T) {
	q, _ := startQueue(t, WithResolution(10*time.Millisecond))

	var which atomic.Int32
	q.Add("dup", 80*time.Millisecond, func() { which.Store(1) })
	q.Add("dup", 30*time.Millisecond, func() { which.Store(2) })

	time.Sleep(200 * time.Millisecond)
	if v := which.Load(); v != 2 {
		t.Errorf("expected replacement callback (2), got %d", v)
	}
}

func TestPending(t *testing.T) {
	q, _ := startQueue(t, WithResolution(10*time.Millisecond))

	q.Add("p1", time.Hour, func() {})
	q.Add("p2", time.Hour, func() {})
	q.Add("p3", time.Hour, func() {})

	time.Sleep(50 * time.Millisecond) // 等命令被消费
	if p := q.Pending(); p != 3 {
		t.Errorf("expected pending=3, got %d", p)
	}

	q.Cancel("p2")
	time.Sleep(50 * time.Millisecond)
	if p := q.Pending(); p != 2 {
		t.Errorf("expected pending=2 after cancel, got %d", p)
	}
}

func TestContextCancel(t *testing.T) {
	q := New(WithResolution(10 * time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- q.Start(ctx) }()
	<-q.Ready()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Start should return nil on ctx cancel, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Start did not return after ctx cancel")
	}
}

func TestPanicRecovery(t *testing.T) {
	var caught atomic.Bool
	q, _ := startQueue(t,
		WithResolution(10*time.Millisecond),
		WithPanicHandler(func(taskID string, r any, stack []byte) {
			caught.Store(true)
		}),
	)

	q.Add("panic-task", 10*time.Millisecond, func() { panic("boom") })

	time.Sleep(100 * time.Millisecond)
	if !caught.Load() {
		t.Error("panic handler was not called")
	}
}

func TestSyncCallback(t *testing.T) {
	q, _ := startQueue(t,
		WithResolution(10*time.Millisecond),
		WithSyncCallback(true),
	)

	var fired atomic.Bool
	q.Add("sync-cb", 20*time.Millisecond, func() { fired.Store(true) })

	time.Sleep(100 * time.Millisecond)
	if !fired.Load() {
		t.Error("sync callback did not fire")
	}
}

func TestAddAt(t *testing.T) {
	q, _ := startQueue(t, WithResolution(10*time.Millisecond))

	var fired atomic.Bool
	at := time.Now().Add(30 * time.Millisecond)
	ok := q.AddAt("at-task", at, func() { fired.Store(true) })
	if !ok {
		t.Fatal("AddAt returned false")
	}

	time.Sleep(120 * time.Millisecond)
	if !fired.Load() {
		t.Error("AddAt task did not fire")
	}
}

func TestString(t *testing.T) {
	q := New(WithName("building"))
	if s := q.String(); s != "timerqueue.Queue(building)" {
		t.Errorf("unexpected String: %s", s)
	}
}

func TestManyTasks(t *testing.T) {
	q, _ := startQueue(t, WithResolution(10*time.Millisecond))

	const n = 500
	var count atomic.Int64
	for i := range n {
		delay := time.Duration(10+i%50) * time.Millisecond
		q.Add("", delay, func() { count.Add(1) })
	}

	time.Sleep(300 * time.Millisecond)
	if c := count.Load(); c != n {
		t.Errorf("expected %d fires, got %d", n, c)
	}
}

func TestCancelNonExistent(t *testing.T) {
	q, _ := startQueue(t, WithResolution(10*time.Millisecond))
	ok := q.Cancel("does-not-exist")
	if !ok {
		t.Error("Cancel should return true (command accepted) even for non-existent ID")
	}
}
