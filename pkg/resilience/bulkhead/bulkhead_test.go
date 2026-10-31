package bulkhead

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRejectWhenFullNoQueue(t *testing.T) {
	b := New(2)
	ctx := context.Background()
	r1, err := b.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := b.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if b.InFlight() != 2 || b.Available() != 0 {
		t.Fatalf("inflight=%d avail=%d", b.InFlight(), b.Available())
	}
	// 第三个应被立即拒绝。
	if _, err := b.Acquire(ctx); !errors.Is(err, ErrFull) {
		t.Fatalf("want ErrFull, got %v", err)
	}
	r1()
	// 释放后应能再次获取。
	r3, err := b.Acquire(ctx)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	r2()
	r3()
	if b.InFlight() != 0 {
		t.Fatalf("inflight should be 0, got %d", b.InFlight())
	}
	if b.Rejected() != 1 {
		t.Fatalf("rejected=%d want 1", b.Rejected())
	}
}

func TestMaxConcurrencyEnforced(t *testing.T) {
	b := New(3, WithQueue(100))
	var cur, peak int32
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = b.Do(context.Background(), func() error {
				c := atomic.AddInt32(&cur, 1)
				for {
					p := atomic.LoadInt32(&peak)
					if c <= p || atomic.CompareAndSwapInt32(&peak, p, c) {
						break
					}
				}
				time.Sleep(2 * time.Millisecond)
				atomic.AddInt32(&cur, -1)
				return nil
			})
		}()
	}
	wg.Wait()
	if peak > 3 {
		t.Fatalf("peak concurrency %d exceeded limit 3", peak)
	}
}

func TestQueueWaitAndAcquire(t *testing.T) {
	b := New(1, WithQueue(1))
	ctx := context.Background()
	r1, _ := b.Acquire(ctx)
	// 一个排队者。
	got := make(chan error, 1)
	go func() {
		r, err := b.Acquire(ctx)
		if err == nil {
			r()
		}
		got <- err
	}()
	// 等它进入等待。
	time.Sleep(20 * time.Millisecond)
	if b.Waiting() != 1 {
		t.Fatalf("waiting=%d want 1", b.Waiting())
	}
	// 队列已满(maxQueue=1),再来一个立即 ErrFull。
	if _, err := b.Acquire(ctx); !errors.Is(err, ErrFull) {
		t.Fatalf("want ErrFull, got %v", err)
	}
	r1() // 放行排队者
	if err := <-got; err != nil {
		t.Fatalf("queued acquire failed: %v", err)
	}
}

func TestAcquireTimeout(t *testing.T) {
	b := New(1, WithQueue(1), WithMaxWait(20*time.Millisecond))
	r1, _ := b.Acquire(context.Background())
	defer r1()
	_, err := b.Acquire(context.Background())
	if !errors.Is(err, ErrAcquireTimeout) {
		t.Fatalf("want ErrAcquireTimeout, got %v", err)
	}
}

func TestContextCancel(t *testing.T) {
	b := New(1, WithQueue(1))
	r1, _ := b.Acquire(context.Background())
	defer r1()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	_, err := b.Acquire(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}
