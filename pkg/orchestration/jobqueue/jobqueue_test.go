package jobqueue

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBasicSubmitAndExecute(t *testing.T) {
	var executed atomic.Bool
	q := New(WithWorkers(2))
	go q.Start(context.Background())
	t.Cleanup(q.Stop)

	time.Sleep(10 * time.Millisecond) // 等 worker 启动

	ok := q.Submit(&Job{
		ID:   "j1",
		Name: "test",
		Fn: func(ctx context.Context, job *Job) error {
			executed.Store(true)
			return nil
		},
	})
	if !ok {
		t.Fatal("submit failed")
	}

	time.Sleep(100 * time.Millisecond)
	if !executed.Load() {
		t.Fatal("job was not executed")
	}
}

func TestPriorityOrdering(t *testing.T) {
	var mu sync.Mutex
	var order []string

	q := New(WithWorkers(1))

	// 先投递多个任务(worker 还没启动)
	q.Submit(&Job{ID: "low", Name: "low", Priority: 10, Fn: func(ctx context.Context, job *Job) error {
		mu.Lock()
		order = append(order, "low")
		mu.Unlock()
		return nil
	}})
	q.Submit(&Job{ID: "high", Name: "high", Priority: 1, Fn: func(ctx context.Context, job *Job) error {
		mu.Lock()
		order = append(order, "high")
		mu.Unlock()
		return nil
	}})
	q.Submit(&Job{ID: "mid", Name: "mid", Priority: 5, Fn: func(ctx context.Context, job *Job) error {
		mu.Lock()
		order = append(order, "mid")
		mu.Unlock()
		return nil
	}})

	go q.Start(context.Background())
	t.Cleanup(q.Stop)

	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 3 {
		t.Fatalf("expected 3 executions, got %d", len(order))
	}
	if order[0] != "high" {
		t.Errorf("expected first=high, got %s", order[0])
	}
	if order[1] != "mid" {
		t.Errorf("expected second=mid, got %s", order[1])
	}
	if order[2] != "low" {
		t.Errorf("expected third=low, got %s", order[2])
	}
}

func TestProgressReporting(t *testing.T) {
	var lastProgress float64
	var progressCount atomic.Int32

	q := New(WithWorkers(1), WithHookFunc(func(e Event) {
		if e.Type == EventProgress {
			lastProgress = e.Job.Progress
			progressCount.Add(1)
		}
	}))
	go q.Start(context.Background())
	t.Cleanup(q.Stop)

	time.Sleep(10 * time.Millisecond)

	q.Submit(&Job{
		ID:   "prog",
		Name: "progress-test",
		Fn: func(ctx context.Context, job *Job) error {
			ReportProgress(ctx, 25)
			ReportProgress(ctx, 50)
			ReportProgress(ctx, 75)
			ReportProgress(ctx, 100)
			return nil
		},
	})

	time.Sleep(100 * time.Millisecond)
	if progressCount.Load() != 4 {
		t.Errorf("expected 4 progress events, got %d", progressCount.Load())
	}
	if lastProgress != 100 {
		t.Errorf("expected last progress=100, got %f", lastProgress)
	}
}

func TestLifecycleEvents(t *testing.T) {
	var mu sync.Mutex
	var events []EventType

	q := New(WithWorkers(1), WithHookFunc(func(e Event) {
		mu.Lock()
		events = append(events, e.Type)
		mu.Unlock()
	}))
	go q.Start(context.Background())
	t.Cleanup(q.Stop)

	time.Sleep(10 * time.Millisecond)

	q.Submit(&Job{
		ID:   "lc",
		Name: "lifecycle",
		Fn: func(ctx context.Context, job *Job) error {
			return nil
		},
	})

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(events) < 3 {
		t.Fatalf("expected at least 3 events, got %d: %v", len(events), events)
	}
	if events[0] != EventSubmit {
		t.Errorf("first event should be Submit, got %s", events[0])
	}
	if events[1] != EventStart {
		t.Errorf("second event should be Start, got %s", events[1])
	}
	if events[2] != EventComplete {
		t.Errorf("third event should be Complete, got %s", events[2])
	}
}

func TestRetryOnFailure(t *testing.T) {
	var attempts atomic.Int32
	q := New(WithWorkers(1))
	go q.Start(context.Background())
	t.Cleanup(q.Stop)

	time.Sleep(10 * time.Millisecond)

	q.Submit(&Job{
		ID:         "retry",
		Name:       "retry-test",
		MaxRetries: 2,
		RetryDelay: 20 * time.Millisecond,
		Fn: func(ctx context.Context, job *Job) error {
			attempts.Add(1)
			if attempts.Load() < 3 {
				return errors.New("transient")
			}
			return nil
		},
	})

	time.Sleep(500 * time.Millisecond)
	if attempts.Load() != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts.Load())
	}
}

func TestDelayedJob(t *testing.T) {
	var executed atomic.Bool
	start := time.Now()

	q := New(WithWorkers(1))
	go q.Start(context.Background())
	t.Cleanup(q.Stop)

	time.Sleep(10 * time.Millisecond)

	q.Submit(&Job{
		ID:    "delayed",
		Name:  "delayed-test",
		Delay: 100 * time.Millisecond,
		Fn: func(ctx context.Context, job *Job) error {
			executed.Store(true)
			return nil
		},
	})

	time.Sleep(50 * time.Millisecond)
	if executed.Load() {
		t.Fatal("job should not execute before delay")
	}

	time.Sleep(150 * time.Millisecond)
	if !executed.Load() {
		t.Fatal("job should have executed after delay")
	}
	elapsed := time.Since(start)
	if elapsed < 100*time.Millisecond {
		t.Errorf("executed too early: %v", elapsed)
	}
}

func TestPauseResume(t *testing.T) {
	var executed atomic.Bool
	q := New(WithWorkers(1))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q.Pause() // 先暂停再启动,确保 worker 不会抢跑

	go q.Start(ctx)
	time.Sleep(20 * time.Millisecond) // 等 worker 启动并进入 pause wait

	q.Submit(&Job{
		ID:   "paused",
		Name: "pause-test",
		Fn: func(ctx context.Context, job *Job) error {
			executed.Store(true)
			return nil
		},
	})

	time.Sleep(100 * time.Millisecond)
	if executed.Load() {
		t.Fatal("should not execute while paused")
	}

	q.Resume()
	time.Sleep(100 * time.Millisecond)
	if !executed.Load() {
		t.Fatal("should execute after resume")
	}

	cancel()
	time.Sleep(50 * time.Millisecond)
}

func TestCancel(t *testing.T) {
	var executed atomic.Bool
	q := New(WithWorkers(1))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q.Pause() // 暂停,让任务停在队列中

	q.Submit(&Job{
		ID:   "cancel-me",
		Name: "cancel-test",
		Fn: func(ctx context.Context, job *Job) error {
			executed.Store(true)
			return nil
		},
	})

	ok := q.Cancel("cancel-me")
	if !ok {
		t.Fatal("cancel should succeed")
	}

	q.Resume()
	go q.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	if executed.Load() {
		t.Fatal("cancelled job should not execute")
	}
	cancel()
	time.Sleep(50 * time.Millisecond)
}

func TestJobTimeout(t *testing.T) {
	var mu sync.Mutex
	var failEvents []Event

	q := New(WithWorkers(1), WithHookFunc(func(e Event) {
		if e.Type == EventFail {
			mu.Lock()
			failEvents = append(failEvents, e)
			mu.Unlock()
		}
	}))
	go q.Start(context.Background())
	t.Cleanup(q.Stop)

	time.Sleep(10 * time.Millisecond)

	q.Submit(&Job{
		ID:      "timeout",
		Name:    "timeout-test",
		Timeout: 50 * time.Millisecond,
		Fn: func(ctx context.Context, job *Job) error {
			<-ctx.Done()
			return ctx.Err()
		},
	})

	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(failEvents) != 1 {
		t.Fatalf("expected 1 fail event, got %d", len(failEvents))
	}
	if !errors.Is(failEvents[0].Err, context.DeadlineExceeded) {
		t.Errorf("expected deadline exceeded, got %v", failEvents[0].Err)
	}
}
