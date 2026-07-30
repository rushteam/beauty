package throttle

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAdd_FlushOnBatch(t *testing.T) {
	var batches [][]int
	var mu sync.Mutex
	th := New[int](func(items []int) {
		mu.Lock()
		batches = append(batches, items)
		mu.Unlock()
	}, WithMaxBatch(3), WithInterval(time.Hour))

	th.Start(context.Background())
	defer th.Stop()

	th.Add(1)
	th.Add(2)
	th.Add(3) // triggers flush

	time.Sleep(10 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(batches) != 1 {
		t.Fatalf("batches = %d, want 1", len(batches))
	}
	if len(batches[0]) != 3 {
		t.Fatalf("batch size = %d, want 3", len(batches[0]))
	}
}

func TestAdd_FlushOnInterval(t *testing.T) {
	var flushed atomic.Int64
	th := New[int](func(items []int) {
		flushed.Add(int64(len(items)))
	}, WithMaxBatch(100), WithInterval(30*time.Millisecond))

	th.Start(context.Background())

	th.Add(1)
	th.Add(2)

	time.Sleep(80 * time.Millisecond)
	th.Stop()

	if flushed.Load() != 2 {
		t.Fatalf("flushed = %d, want 2", flushed.Load())
	}
}

func TestStop_FlushesRemaining(t *testing.T) {
	var flushed atomic.Int64
	th := New[int](func(items []int) {
		flushed.Add(int64(len(items)))
	}, WithMaxBatch(100), WithInterval(time.Hour))

	th.Start(context.Background())

	th.Add(1)
	th.Add(2)
	th.Add(3)
	th.Stop()

	if flushed.Load() != 3 {
		t.Fatalf("flushed on stop = %d, want 3", flushed.Load())
	}
}

func TestFlush_Manual(t *testing.T) {
	var flushed atomic.Int64
	th := New[int](func(items []int) {
		flushed.Add(int64(len(items)))
	}, WithMaxBatch(100), WithInterval(time.Hour))

	th.Start(context.Background())
	defer th.Stop()

	th.Add(1)
	th.Add(2)
	th.Flush()

	if flushed.Load() != 2 {
		t.Fatalf("manual flush = %d, want 2", flushed.Load())
	}
}

func TestFlush_EmptyNoop(t *testing.T) {
	var calls atomic.Int64
	th := New[int](func(items []int) {
		calls.Add(1)
	}, WithMaxBatch(10), WithInterval(time.Hour))

	th.Start(context.Background())
	defer th.Stop()

	th.Flush() // nothing buffered

	if calls.Load() != 0 {
		t.Fatalf("flush called on empty buffer")
	}
}

func TestLen(t *testing.T) {
	th := New[int](func(items []int) {}, WithMaxBatch(100), WithInterval(time.Hour))
	th.Start(context.Background())
	defer th.Stop()

	th.Add(1)
	th.Add(2)
	if th.Len() != 2 {
		t.Fatalf("Len = %d, want 2", th.Len())
	}
}

func TestAddBatch(t *testing.T) {
	var batches [][]int
	var mu sync.Mutex
	th := New[int](func(items []int) {
		mu.Lock()
		batches = append(batches, append([]int(nil), items...))
		mu.Unlock()
	}, WithMaxBatch(3), WithInterval(time.Hour))

	th.Start(context.Background())

	th.AddBatch([]int{1, 2, 3, 4, 5})
	th.Stop()

	mu.Lock()
	defer mu.Unlock()
	// 5 items with maxBatch=3: flush at 3 + remaining 2 on stop
	total := 0
	for _, b := range batches {
		total += len(b)
	}
	if total != 5 {
		t.Fatalf("total flushed = %d, want 5", total)
	}
}

func TestConcurrent(t *testing.T) {
	var total atomic.Int64
	th := New[int](func(items []int) {
		total.Add(int64(len(items)))
	}, WithMaxBatch(7), WithInterval(20*time.Millisecond))

	th.Start(context.Background())

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			th.Add(n)
		}(i)
	}
	wg.Wait()
	th.Stop()

	if total.Load() != 100 {
		t.Fatalf("total = %d, want 100", total.Load())
	}
}

func TestContextCancel(t *testing.T) {
	var flushed atomic.Int64
	th := New[int](func(items []int) {
		flushed.Add(int64(len(items)))
	}, WithMaxBatch(100), WithInterval(time.Hour))

	ctx, cancel := context.WithCancel(context.Background())
	th.Start(ctx)

	th.Add(1)
	th.Add(2)
	cancel()
	time.Sleep(20 * time.Millisecond)

	// Stop should still flush remaining
	th.Stop()
	if flushed.Load() != 2 {
		t.Fatalf("flushed after cancel = %d, want 2", flushed.Load())
	}
}
