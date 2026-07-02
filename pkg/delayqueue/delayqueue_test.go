package delayqueue_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/delayqueue"
)

func TestSchedule_FiresAfterDelay(t *testing.T) {
	q := delayqueue.New()
	defer q.Stop()

	fired := make(chan time.Time, 1)
	start := time.Now()
	q.Schedule("k", 30*time.Millisecond, func() { fired <- time.Now() })

	select {
	case at := <-fired:
		if d := at.Sub(start); d < 25*time.Millisecond {
			t.Fatalf("fired too early: %v", d)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("task did not fire")
	}
}

func TestSchedule_OrderByFireTime(t *testing.T) {
	q := delayqueue.New()
	defer q.Stop()

	var mu sync.Mutex
	var order []string
	done := make(chan struct{})
	add := func(name string) func() {
		return func() {
			mu.Lock()
			order = append(order, name)
			n := len(order)
			mu.Unlock()
			if n == 3 {
				close(done)
			}
		}
	}
	// 乱序注册,应按到期时间触发
	q.Schedule("c", 60*time.Millisecond, add("c"))
	q.Schedule("a", 20*time.Millisecond, add("a"))
	q.Schedule("b", 40*time.Millisecond, add("b"))

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("not all fired")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 3 || order[0] != "a" || order[1] != "b" || order[2] != "c" {
		t.Fatalf("wrong order: %v", order)
	}
}

func TestCancel_PreventsFire(t *testing.T) {
	q := delayqueue.New()
	defer q.Stop()

	var fired atomic.Bool
	q.Schedule("k", 30*time.Millisecond, func() { fired.Store(true) })
	if !q.Cancel("k") {
		t.Fatal("cancel should report true for existing task")
	}
	time.Sleep(60 * time.Millisecond)
	if fired.Load() {
		t.Fatal("cancelled task should not fire")
	}
	if q.Cancel("k") {
		t.Fatal("cancel of gone task should report false")
	}
}

func TestSchedule_RescheduleOverwrites(t *testing.T) {
	q := delayqueue.New()
	defer q.Stop()

	var count atomic.Int64
	q.Schedule("k", 20*time.Millisecond, func() { count.Add(1) })
	// 改期:覆盖上一个,应只触发一次(用更晚的时间)
	replaced := q.Schedule("k", 50*time.Millisecond, func() { count.Add(1) })
	if !replaced {
		t.Fatal("reschedule should report replaced=true")
	}
	time.Sleep(100 * time.Millisecond)
	if count.Load() != 1 {
		t.Fatalf("reschedule should fire once, fired %d", count.Load())
	}
}

func TestSchedule_ManyConcurrent(t *testing.T) {
	q := delayqueue.New()
	defer q.Stop()

	const n = 200
	var fired atomic.Int64
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			q.Schedule(string(rune('a'+i%26))+string(rune('0'+i/26)),
				time.Duration(i%20)*time.Millisecond, func() { fired.Add(1) })
		})
	}
	wg.Wait()
	time.Sleep(200 * time.Millisecond)
	if fired.Load() != n {
		t.Fatalf("want %d fired, got %d", n, fired.Load())
	}
}

func TestPanic_Recovered(t *testing.T) {
	var gotKey atomic.Value
	q := delayqueue.New(delayqueue.WithOnPanic(func(key string, err error) {
		gotKey.Store(key)
	}))
	defer q.Stop()

	q.Schedule("boom", 10*time.Millisecond, func() { panic("kaboom") })
	// 再排一个正常任务,验证驱动循环没被 panic 带崩
	ok := make(chan struct{})
	q.Schedule("ok", 30*time.Millisecond, func() { close(ok) })

	select {
	case <-ok:
	case <-time.After(time.Second):
		t.Fatal("queue died after panic in a task")
	}
	if k, _ := gotKey.Load().(string); k != "boom" {
		t.Fatalf("onPanic key = %q, want boom", k)
	}
}

func TestStop_Idempotent(t *testing.T) {
	q := delayqueue.New()
	q.Stop()
	q.Stop() // 不应 panic
}
