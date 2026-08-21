package mq

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBatch_FlushOnSize(t *testing.T) {
	var mu sync.Mutex
	var batches [][]Message

	h := Batch(3, 5*time.Second, func(ctx context.Context, msgs []Message) error {
		mu.Lock()
		batches = append(batches, msgs)
		mu.Unlock()
		return nil
	})

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := h(ctx, Message{Topic: "t", Body: []byte{byte(i)}}); err != nil {
			t.Fatal(err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(batches) != 1 {
		t.Fatalf("expected 1 batch, got %d", len(batches))
	}
	if len(batches[0]) != 3 {
		t.Fatalf("expected batch size 3, got %d", len(batches[0]))
	}
}

func TestBatch_FlushOnTimeout(t *testing.T) {
	var mu sync.Mutex
	var batches [][]Message

	h := Batch(100, 50*time.Millisecond, func(ctx context.Context, msgs []Message) error {
		mu.Lock()
		batches = append(batches, msgs)
		mu.Unlock()
		return nil
	})

	ctx := context.Background()
	_ = h(ctx, Message{Topic: "t", Body: []byte("hello")})

	// 未满,不应立即 flush
	mu.Lock()
	if len(batches) != 0 {
		t.Fatal("should not flush before timeout")
	}
	mu.Unlock()

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(batches) != 1 {
		t.Fatalf("expected 1 batch after timeout, got %d", len(batches))
	}
	if len(batches[0]) != 1 {
		t.Fatalf("expected 1 msg in batch, got %d", len(batches[0]))
	}
}

func TestBatch_PanicOnNilFn(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil fn")
		}
	}()
	Batch(10, time.Second, nil)
}

func TestBatchCollector_SizeFlush(t *testing.T) {
	var mu sync.Mutex
	var batches [][]Message

	bc := NewBatchCollector(3, 5*time.Second, func(ctx context.Context, msgs []Message) error {
		mu.Lock()
		batches = append(batches, msgs)
		mu.Unlock()
		return nil
	})

	h := bc.Handler()
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_ = h(ctx, Message{Topic: "t", Body: []byte{byte(i)}})
	}

	mu.Lock()
	defer mu.Unlock()
	if len(batches) != 1 {
		t.Fatalf("expected 1 batch (size trigger), got %d", len(batches))
	}
	if len(batches[0]) != 3 {
		t.Errorf("expected batch of 3, got %d", len(batches[0]))
	}
	// 剩余 2 条在 buffer
	if bc.Pending() != 2 {
		t.Errorf("expected 2 pending, got %d", bc.Pending())
	}
}

func TestBatchCollector_TimeoutFlush(t *testing.T) {
	var flushCount atomic.Int32

	bc := NewBatchCollector(100, 50*time.Millisecond, func(ctx context.Context, msgs []Message) error {
		flushCount.Add(1)
		return nil
	})

	h := bc.Handler()
	_ = h(context.Background(), Message{Topic: "t", Body: []byte("x")})

	ctx, cancel := context.WithCancel(context.Background())
	go bc.Start(ctx)

	time.Sleep(100 * time.Millisecond)
	cancel()
	time.Sleep(50 * time.Millisecond)

	if flushCount.Load() < 1 {
		t.Error("expected at least 1 timeout flush")
	}
}

func TestBatchCollector_GracefulShutdown(t *testing.T) {
	var mu sync.Mutex
	var total int

	bc := NewBatchCollector(100, time.Hour, func(ctx context.Context, msgs []Message) error {
		mu.Lock()
		total += len(msgs)
		mu.Unlock()
		return nil
	})

	h := bc.Handler()
	for i := 0; i < 5; i++ {
		_ = h(context.Background(), Message{Topic: "t"})
	}

	ctx, cancel := context.WithCancel(context.Background())
	go bc.Start(ctx)
	time.Sleep(20 * time.Millisecond)
	cancel() // 触发 graceful shutdown flush
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if total != 5 {
		t.Errorf("expected 5 flushed on shutdown, got %d", total)
	}
}
