package redisqueue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func setup(t *testing.T) (*Queue, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	q := New(rdb, "test",
		WithPollInterval(20*time.Millisecond),
		WithDelayResolution(20*time.Millisecond),
		WithVisibilityTime(200*time.Millisecond),
	)
	return q, mr
}

func TestSubmitAndProcess(t *testing.T) {
	q, _ := setup(t)
	ctx := context.Background()

	err := q.Submit(ctx, &Job{
		ID:      "job-1",
		Name:    "greet",
		Payload: []byte(`"hello"`),
	})
	if err != nil {
		t.Fatal(err)
	}

	pending, _ := q.Pending(ctx)
	if pending != 1 {
		t.Fatalf("expected 1 pending, got %d", pending)
	}

	var processed atomic.Bool
	workerCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()

	go q.StartWorker(workerCtx, func(ctx context.Context, job *Job) error {
		if job.ID != "job-1" {
			t.Errorf("unexpected job ID: %s", job.ID)
		}
		processed.Store(true)
		return nil
	})

	time.Sleep(100 * time.Millisecond)
	if !processed.Load() {
		t.Error("job should have been processed")
	}

	job, err := q.GetJob(ctx, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if job.State != StateCompleted {
		t.Errorf("expected completed, got %s", job.State)
	}
}

func TestPriorityOrder(t *testing.T) {
	q, _ := setup(t)
	ctx := context.Background()

	// 高优先级(低数值)应先执行
	q.Submit(ctx, &Job{ID: "low", Name: "low", Priority: 10, Payload: []byte("low")})
	q.Submit(ctx, &Job{ID: "high", Name: "high", Priority: 1, Payload: []byte("high")})
	q.Submit(ctx, &Job{ID: "mid", Name: "mid", Priority: 5, Payload: []byte("mid")})

	var mu sync.Mutex
	var order []string
	workerCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()

	go q.StartWorker(workerCtx, func(ctx context.Context, job *Job) error {
		mu.Lock()
		order = append(order, job.Name)
		mu.Unlock()
		return nil
	})

	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 3 {
		t.Fatalf("expected 3, got %d: %v", len(order), order)
	}
	if order[0] != "high" || order[1] != "mid" || order[2] != "low" {
		t.Errorf("unexpected order: %v", order)
	}
}

func TestDelayedJob(t *testing.T) {
	q, _ := setup(t)
	ctx := context.Background()

	q.Submit(ctx, &Job{
		ID:    "delayed-1",
		Name:  "delayed",
		Delay: 80 * time.Millisecond,
	})

	pending, _ := q.Pending(ctx)
	if pending != 0 {
		t.Errorf("should not be in waiting yet, got pending=%d", pending)
	}
	delayed, _ := q.Delayed(ctx)
	if delayed != 1 {
		t.Errorf("expected 1 delayed, got %d", delayed)
	}

	var processed atomic.Bool
	workerCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	go q.StartWorker(workerCtx, func(ctx context.Context, job *Job) error {
		processed.Store(true)
		return nil
	})

	time.Sleep(50 * time.Millisecond)
	if processed.Load() {
		t.Error("should not process before delay")
	}

	time.Sleep(150 * time.Millisecond)
	if !processed.Load() {
		t.Error("should process after delay")
	}
}

func TestRetryOnFailure(t *testing.T) {
	q, _ := setup(t)
	ctx := context.Background()

	q.Submit(ctx, &Job{
		ID:         "retry-1",
		Name:       "flaky",
		MaxRetries: 2,
		RetryDelay: 30 * time.Millisecond,
	})

	var attempts atomic.Int32
	workerCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	go q.StartWorker(workerCtx, func(ctx context.Context, job *Job) error {
		n := attempts.Add(1)
		if n < 3 {
			return errors.New("transient error")
		}
		return nil
	})

	time.Sleep(400 * time.Millisecond)
	if attempts.Load() != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts.Load())
	}

	job, _ := q.GetJob(ctx, "retry-1")
	if job.State != StateCompleted {
		t.Errorf("expected completed after retries, got %s", job.State)
	}
}

func TestJobFailsAfterMaxRetries(t *testing.T) {
	q, _ := setup(t)
	ctx := context.Background()

	q.Submit(ctx, &Job{
		ID:         "fail-1",
		Name:       "always-fail",
		MaxRetries: 1,
		RetryDelay: 20 * time.Millisecond,
	})

	workerCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()

	go q.StartWorker(workerCtx, func(ctx context.Context, job *Job) error {
		return errors.New("permanent error")
	})

	time.Sleep(250 * time.Millisecond)

	job, _ := q.GetJob(ctx, "fail-1")
	if job.State != StateFailed {
		t.Errorf("expected failed, got %s", job.State)
	}
	if job.Attempts != 2 { // 1 initial + 1 retry
		t.Errorf("expected 2 attempts, got %d", job.Attempts)
	}
}

func TestEventHook(t *testing.T) {
	var mu sync.Mutex
	var events []EventType
	q, _ := setup(t)
	q.cfg.Hook = func(e Event) {
		mu.Lock()
		events = append(events, e.Type)
		mu.Unlock()
	}
	ctx := context.Background()

	q.Submit(ctx, &Job{ID: "ev-1", Name: "event-test"})

	workerCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	go q.StartWorker(workerCtx, func(ctx context.Context, job *Job) error {
		return nil
	})

	time.Sleep(150 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(events) < 3 {
		t.Fatalf("expected at least 3 events, got %d: %v", len(events), events)
	}
	if events[0] != EventSubmit {
		t.Errorf("first event should be submit, got %s", events[0])
	}
	if events[1] != EventStart {
		t.Errorf("second event should be start, got %s", events[1])
	}
	if events[2] != EventComplete {
		t.Errorf("third event should be complete, got %s", events[2])
	}
}

func TestGetJob(t *testing.T) {
	q, _ := setup(t)
	ctx := context.Background()

	q.Submit(ctx, &Job{
		ID:       "info-1",
		Name:     "info",
		Priority: 7,
		Payload:  []byte(`{"x":1}`),
	})

	job, err := q.GetJob(ctx, "info-1")
	if err != nil {
		t.Fatal(err)
	}
	if job.Priority != 7 {
		t.Errorf("expected priority 7, got %d", job.Priority)
	}

	var payload map[string]int
	json.Unmarshal(job.Payload, &payload)
	if payload["x"] != 1 {
		t.Error("payload mismatch")
	}
}

func TestClean(t *testing.T) {
	q, _ := setup(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		q.Submit(ctx, &Job{ID: "clean-" + string(rune('0'+i)), Name: "clean"})
	}

	workerCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	go q.StartWorker(workerCtx, func(ctx context.Context, job *Job) error {
		return nil
	})
	time.Sleep(150 * time.Millisecond)

	removed, err := q.Clean(ctx, StateCompleted, 2)
	if err != nil {
		t.Fatal(err)
	}
	if removed < 1 {
		t.Error("expected some jobs to be cleaned")
	}
}

func TestWaitingScoreBands(t *testing.T) {
	if waitingScore(0, 2) <= waitingScore(-1, 1) {
		t.Fatal("higher priority should sort first")
	}
	if waitingScore(10, 1) >= waitingScore(10, 2) {
		t.Fatal("same priority should follow arrival")
	}
	if waitingScore(0, priorityBand-1) >= waitingScore(1, 1) {
		t.Fatal("sequence must stay inside its priority band")
	}
}

func TestSamePriorityKeepsArrivalOrder(t *testing.T) {
	q, _ := setup(t)
	ctx := context.Background()
	for _, job := range []*Job{
		{ID: "a", Priority: 5},
		{ID: "b", Priority: 5},
		{ID: "c", Priority: 1},
		{ID: "d", Priority: 5},
	} {
		if err := q.Submit(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	got := drain(t, q)
	want := []string{"c", "a", "b", "d"}
	if !sameIDs(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestRatioInsertsVIPAmongNormal(t *testing.T) {
	base, _ := setup(t)
	q := New(base.rdb, "ratio", WithRatio(
		Share{Priority: 10, Count: 5},
		Share{Priority: 0, Count: 1},
	))
	ctx := context.Background()
	for _, id := range []string{"v1", "v2", "v3"} {
		if err := q.Submit(ctx, &Job{ID: id, Priority: 0}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 8; i++ {
		if err := q.Submit(ctx, &Job{ID: fmt.Sprintf("n%d", i), Priority: 10}); err != nil {
			t.Fatal(err)
		}
	}
	got := drain(t, q)
	want := []string{"n1", "n2", "n3", "n4", "n5", "v1", "n6", "n7", "n8", "v2", "v3"}
	if !sameIDs(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestRatioSkipsEmptyClass(t *testing.T) {
	base, _ := setup(t)
	q := New(base.rdb, "ratio-vip", WithRatio(
		Share{Priority: 10, Count: 5},
		Share{Priority: 0, Count: 1},
	))
	ctx := context.Background()
	for _, id := range []string{"v1", "v2", "v3"} {
		if err := q.Submit(ctx, &Job{ID: id, Priority: 0}); err != nil {
			t.Fatal(err)
		}
	}
	got := drain(t, q)
	want := []string{"v1", "v2", "v3"}
	if !sameIDs(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestRatioServesUnlistedPriority(t *testing.T) {
	base, _ := setup(t)
	q := New(base.rdb, "ratio-extra", WithRatio(
		Share{Priority: 10, Count: 2},
		Share{Priority: 0, Count: 1},
	))
	ctx := context.Background()
	for _, job := range []*Job{
		{ID: "n1", Priority: 10},
		{ID: "n2", Priority: 10},
		{ID: "v1", Priority: 0},
		{ID: "u1", Priority: 3},
	} {
		if err := q.Submit(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	got := drain(t, q)
	want := []string{"n1", "n2", "v1", "u1"}
	if !sameIDs(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestRequeueKeepsOrDropsPlace(t *testing.T) {
	q, _ := setup(t)
	ctx := context.Background()
	if err := q.Submit(ctx, &Job{ID: "a", Priority: 5}); err != nil {
		t.Fatal(err)
	}
	if err := q.Submit(ctx, &Job{ID: "b", Priority: 5}); err != nil {
		t.Fatal(err)
	}
	id, err := q.dequeue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if id != "a" {
		t.Fatalf("first = %s", id)
	}

	kept, err := q.GetJob(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if err := q.rdb.SRem(ctx, q.key("active"), "a").Err(); err != nil {
		t.Fatal(err)
	}
	if err := q.pushWaiting(ctx, kept, false); err != nil {
		t.Fatal(err)
	}
	if got := drain(t, q); !sameIDs(got, []string{"a", "b"}) {
		t.Fatalf("stall requeue order = %v", got)
	}

	if err := q.Submit(ctx, &Job{ID: "c", Priority: 5}); err != nil {
		t.Fatal(err)
	}
	if err := q.Submit(ctx, &Job{ID: "d", Priority: 5}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.dequeue(ctx); err != nil {
		t.Fatal(err)
	}
	retried, err := q.GetJob(ctx, "c")
	if err != nil {
		t.Fatal(err)
	}
	if err := q.rdb.SRem(ctx, q.key("active"), "c").Err(); err != nil {
		t.Fatal(err)
	}
	if err := q.pushWaiting(ctx, retried, true); err != nil {
		t.Fatal(err)
	}
	if got := drain(t, q); !sameIDs(got, []string{"d", "c"}) {
		t.Fatalf("retry order = %v", got)
	}
}

func TestDelayedJobJoinsTailOfSamePriority(t *testing.T) {
	q, _ := setup(t)
	ctx := context.Background()
	if err := q.Submit(ctx, &Job{ID: "early", Priority: 5, Delay: 20 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	if err := q.Submit(ctx, &Job{ID: "late", Priority: 5}); err != nil {
		t.Fatal(err)
	}

	got := make(chan string, 2)
	workerCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	go q.StartWorker(workerCtx, func(ctx context.Context, job *Job) error {
		got <- job.ID
		return nil
	})

	order := make([]string, 0, 2)
	timeout := time.NewTimer(800 * time.Millisecond)
	defer timeout.Stop()
	for len(order) < 2 {
		select {
		case id := <-got:
			order = append(order, id)
		case <-timeout.C:
			t.Fatalf("order = %v", order)
		}
	}
	if order[0] != "late" || order[1] != "early" {
		t.Fatalf("order = %v", order)
	}
}

func drain(t *testing.T, q *Queue) []string {
	t.Helper()
	ctx := context.Background()
	got := []string{}
	for {
		id, err := q.dequeue(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if id == "" {
			return got
		}
		got = append(got, id)
		if len(got) > 32 {
			t.Fatalf("dequeue did not stop: %v", got)
		}
	}
}

func sameIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
