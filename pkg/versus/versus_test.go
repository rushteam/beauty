package versus_test

import (
	"sync"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/versus"
)

func TestFlow_AddAndLeader(t *testing.T) {
	m := versus.New("pk-1", []string{"A", "B"}, versus.WithDuration(time.Hour))
	defer m.Close()

	if err := m.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := m.Add("A", 100); err != nil {
		t.Fatalf("add A: %v", err)
	}
	if _, err := m.Add("B", 50); err != nil {
		t.Fatalf("add B: %v", err)
	}
	snap := m.Snapshot()
	if snap.Scores["A"] != 100 || snap.Scores["B"] != 50 {
		t.Fatalf("scores = %v", snap.Scores)
	}
	if snap.Leader != "A" || snap.Tie {
		t.Fatalf("leader = %q tie=%v, want A", snap.Leader, snap.Tie)
	}
	if !snap.Running {
		t.Fatal("should be running")
	}
}

func TestAdd_RejectedBeforeStartAndAfterEnd(t *testing.T) {
	m := versus.New("pk", []string{"A", "B"}, versus.WithDuration(time.Hour))
	defer m.Close()

	if _, err := m.Add("A", 1); err != versus.ErrNotRunning {
		t.Fatalf("add before start: %v, want ErrNotRunning", err)
	}
	m.Start()
	m.Finish()
	if _, err := m.Add("A", 1); err != versus.ErrNotRunning {
		t.Fatalf("add after end: %v, want ErrNotRunning", err)
	}
}

func TestAdd_UnknownSide(t *testing.T) {
	m := versus.New("pk", []string{"A", "B"})
	defer m.Close()
	m.Start()
	if _, err := m.Add("C", 1); err != versus.ErrUnknownSide {
		t.Fatalf("unknown side: %v", err)
	}
}

func TestStart_Idempotentish(t *testing.T) {
	m := versus.New("pk", []string{"A", "B"}, versus.WithDuration(time.Hour))
	defer m.Close()
	if err := m.Start(); err != nil {
		t.Fatalf("first start: %v", err)
	}
	if err := m.Start(); err == nil {
		t.Fatal("second start should error (invalid transition)")
	}
}

func TestFinish_OnEndWinner(t *testing.T) {
	var got versus.Result
	var wg sync.WaitGroup
	wg.Add(1)
	m := versus.New("pk", []string{"A", "B"},
		versus.WithDuration(time.Hour),
		versus.WithOnEnd(func(r versus.Result) { got = r; wg.Done() }))
	defer m.Close()

	m.Start()
	m.Add("A", 30)
	m.Add("B", 70)
	m.Finish()
	wg.Wait()

	if got.Winner != "B" || got.Tie {
		t.Fatalf("winner = %q tie=%v, want B", got.Winner, got.Tie)
	}
}

func TestFinish_Tie(t *testing.T) {
	var got versus.Result
	var wg sync.WaitGroup
	wg.Add(1)
	m := versus.New("pk", []string{"A", "B"},
		versus.WithDuration(time.Hour),
		versus.WithOnEnd(func(r versus.Result) { got = r; wg.Done() }))
	defer m.Close()

	m.Start()
	m.Add("A", 50)
	m.Add("B", 50)
	m.Finish()
	wg.Wait()

	if !got.Tie || got.Winner != "" {
		t.Fatalf("want tie, got winner=%q tie=%v", got.Winner, got.Tie)
	}
}

func TestFinish_Idempotent(t *testing.T) {
	var calls int
	m := versus.New("pk", []string{"A", "B"},
		versus.WithDuration(time.Hour),
		versus.WithOnEnd(func(r versus.Result) { calls++ }))
	defer m.Close()
	m.Start()
	m.Finish()
	m.Finish()
	m.Finish()
	// OnEnd 只应触发一次
	time.Sleep(10 * time.Millisecond)
	if calls != 1 {
		t.Fatalf("OnEnd called %d times, want 1", calls)
	}
}

func TestAutoFinishOnTimeout(t *testing.T) {
	done := make(chan versus.Result, 1)
	m := versus.New("pk", []string{"A", "B"},
		versus.WithDuration(40*time.Millisecond),
		versus.WithOnEnd(func(r versus.Result) { done <- r }))
	defer m.Close()

	m.Start()
	m.Add("A", 10)
	select {
	case r := <-done:
		if r.Winner != "A" {
			t.Fatalf("timeout winner = %q, want A", r.Winner)
		}
	case <-time.After(time.Second):
		t.Fatal("did not auto-finish on timeout")
	}
	if !m.Snapshot().Ended {
		t.Fatal("should be ended after timeout")
	}
}

func TestSubscribe_ReceivesEvents(t *testing.T) {
	m := versus.New("pk", []string{"A", "B"}, versus.WithDuration(time.Hour))
	defer m.Close()

	ch, unsub := m.Subscribe(t.Context())
	defer unsub()

	m.Start()
	m.Add("A", 5)
	m.Finish()

	// 应至少收到 started / score_changed / ended。
	types := map[versus.EventType]bool{}
	timeout := time.After(time.Second)
	for len(types) < 3 {
		select {
		case ev := <-ch:
			types[ev.Type] = true
		case <-timeout:
			t.Fatalf("only got events: %v", types)
		}
	}
	if !types[versus.EventStarted] || !types[versus.EventScoreChanged] || !types[versus.EventEnded] {
		t.Fatalf("missing event types: %v", types)
	}
}

func TestConcurrentAdd(t *testing.T) {
	m := versus.New("pk", []string{"A", "B"}, versus.WithDuration(time.Hour), versus.WithEventBuffer(1024))
	defer m.Close()
	m.Start()

	const goroutines, per = 20, 500
	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range per {
				m.Add("A", 1)
			}
		})
	}
	wg.Wait()
	if got := m.Snapshot().Scores["A"]; got != goroutines*per {
		t.Fatalf("concurrent score = %d, want %d", got, goroutines*per)
	}
}
