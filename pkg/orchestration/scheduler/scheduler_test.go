package scheduler

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestScheduler_Basic(t *testing.T) {
	s := New(WithWorkers(2), WithQueueSize(16))
	s.Start(context.Background())
	defer func() { s.Stop(); s.Wait() }()

	var done atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		s.Submit(&Task{Name: "t", Fn: func(ctx context.Context) error {
			defer wg.Done()
			done.Add(1)
			return nil
		}})
	}
	wg.Wait()
	if done.Load() != 10 {
		t.Fatalf("done=%d want 10", done.Load())
	}
}

func TestScheduler_PauseResume(t *testing.T) {
	s := New(WithWorkers(1), WithQueueSize(16))
	s.Start(context.Background())
	defer func() { s.Stop(); s.Wait() }()

	var done atomic.Int32

	// 先确保 worker 启动并空闲:投一个慢任务占住 worker,趁机 Pause。
	gate := make(chan struct{})
	s.Submit(&Task{Fn: func(ctx context.Context) error {
		done.Add(1)
		<-gate // 阻塞直到放行
		return nil
	}})
	time.Sleep(50 * time.Millisecond) // 等 worker 取走并执行
	if done.Load() != 1 {
		t.Fatalf("first task not running, done=%d", done.Load())
	}

	// 此时 worker 正忙,Pause 后放行第一个任务,第二个应不被取走。
	s.Pause()
	close(gate) // 放行第一个,它执行完

	s.Submit(&Task{Fn: func(ctx context.Context) error {
		done.Add(1)
		return nil
	}})
	// 暂停期间第二个不应执行。
	time.Sleep(150 * time.Millisecond)
	if done.Load() != 1 {
		t.Fatalf("during pause done=%d want 1", done.Load())
	}

	s.Resume()
	time.Sleep(200 * time.Millisecond)
	if done.Load() != 2 {
		t.Fatalf("after resume done=%d want 2", done.Load())
	}
}

func TestScheduler_TrySubmitFull(t *testing.T) {
	s := New(WithWorkers(0), WithQueueSize(2)) // 0 worker 不会消费
	s.Start(context.Background())
	defer func() { s.Stop(); s.Wait() }()

	if !s.TrySubmit(&Task{Fn: func(context.Context) error { return nil }}) {
		t.Fatal("first TrySubmit should succeed")
	}
	if !s.TrySubmit(&Task{Fn: func(context.Context) error { return nil }}) {
		t.Fatal("second TrySubmit should succeed")
	}
	if s.TrySubmit(&Task{Fn: func(context.Context) error { return nil }}) {
		t.Fatal("third TrySubmit should fail (queue full)")
	}
}

func TestScheduler_PanicRecovery(t *testing.T) {
	var panicCount atomic.Int32
	s := New(WithWorkers(1), WithErrorHandler(func(name string, err error, stack []byte) {
		if err != nil {
			panicCount.Add(1)
		}
	}))
	s.Start(context.Background())
	defer func() { s.Stop(); s.Wait() }()

	var wg sync.WaitGroup
	wg.Add(2)
	s.Submit(&Task{Name: "panic", Fn: func(ctx context.Context) error {
		defer wg.Done()
		panic("boom")
	}})
	s.Submit(&Task{Name: "err", Fn: func(ctx context.Context) error {
		defer wg.Done()
		return context.Canceled
	}})
	wg.Wait()
	time.Sleep(50 * time.Millisecond)
	if panicCount.Load() != 1 {
		t.Fatalf("error count=%d want 1 (only the error, not panic)", panicCount.Load())
	}
}
