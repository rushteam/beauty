package worker_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/orchestration/worker"
	"github.com/rushteam/beauty/pkg/store/dlock"
)

func TestNewTicker_Periodic(t *testing.T) {
	var count atomic.Int32
	svc := worker.NewTicker("test", 20*time.Millisecond, func(_ context.Context) {
		count.Add(1)
	})

	if s := svc.String(); s != "ticker(test/20ms)" {
		t.Fatalf("String()=%q", s)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	_ = svc.Start(ctx)

	if got := count.Load(); got < 2 || got > 5 {
		t.Fatalf("expected 2-5 ticks, got %d", got)
	}
}

func TestNewTicker_Immediate(t *testing.T) {
	var count atomic.Int32
	svc := worker.NewTicker("imm", 1*time.Hour, func(_ context.Context) {
		count.Add(1)
	}, worker.WithImmediate())

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_ = svc.Start(ctx)

	if got := count.Load(); got != 1 {
		t.Fatalf("expected 1 immediate call, got %d", got)
	}
}

func TestNewLeaderTicker(t *testing.T) {
	var count atomic.Int32
	elector := dlock.NewMemory()
	svc := worker.NewLeaderTicker("leader-test", elector, 20*time.Millisecond, func(_ context.Context) {
		count.Add(1)
	})

	if s := svc.String(); s != "leader-ticker(leader-test/20ms)" {
		t.Fatalf("String()=%q", s)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	_ = svc.Start(ctx)

	if got := count.Load(); got < 1 {
		t.Fatalf("expected at least 1 tick, got %d", got)
	}
}

func TestNewLeaderTicker_Immediate(t *testing.T) {
	var count atomic.Int32
	elector := dlock.NewMemory()
	svc := worker.NewLeaderTicker("leader-imm", elector, 1*time.Hour, func(_ context.Context) {
		count.Add(1)
	}, worker.WithImmediate())

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_ = svc.Start(ctx)

	if got := count.Load(); got < 1 {
		t.Fatalf("expected at least 1 immediate call, got %d", got)
	}
}
