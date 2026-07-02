package tally_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/tally"
)

func TestAggregatesAndFlushes(t *testing.T) {
	var mu sync.Mutex
	got := map[string]int64{}
	flushes := 0
	tl := tally.New(func(ctx context.Context, batch map[string]int64) {
		mu.Lock()
		for k, v := range batch {
			got[k] += v
		}
		flushes++
		mu.Unlock()
	}, tally.WithFlushInterval(20*time.Millisecond))

	for range 1000 {
		tl.Add("room:1", 1)
		tl.Add("room:2", 2)
	}
	tl.Stop() // 含最后一次 flush

	mu.Lock()
	defer mu.Unlock()
	if got["room:1"] != 1000 || got["room:2"] != 2000 {
		t.Fatalf("aggregated = %v, want room:1=1000 room:2=2000", got)
	}
	// 关键:1000+1000 次 Add 只触发了远少于此的 flush。
	if flushes == 0 || flushes > 50 {
		t.Fatalf("flush count = %d, expected a small number (write coalescing)", flushes)
	}
}

func TestMaxKeysTriggersFlush(t *testing.T) {
	var flushed atomic.Int64
	done := make(chan struct{}, 1)
	tl := tally.New(func(ctx context.Context, batch map[string]int64) {
		flushed.Add(int64(len(batch)))
		select {
		case done <- struct{}{}:
		default:
		}
	}, tally.WithFlushInterval(time.Hour), tally.WithMaxKeys(5)) // 间隔极大,只能靠 maxKeys 触发
	defer tl.Stop()

	for i := range 5 {
		tl.Add(string(rune('a'+i)), 1)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("maxKeys did not trigger flush")
	}
	if flushed.Load() < 5 {
		t.Fatalf("flushed keys = %d, want >=5", flushed.Load())
	}
}

func TestFlushOnStop(t *testing.T) {
	var got int64
	tl := tally.New(func(ctx context.Context, batch map[string]int64) {
		atomic.AddInt64(&got, batch["k"])
	}, tally.WithFlushInterval(time.Hour)) // 不会定时触发

	tl.Add("k", 42)
	tl.Stop() // 应触发尾部 flush

	if atomic.LoadInt64(&got) != 42 {
		t.Fatalf("flush-on-stop lost data: got %d, want 42", got)
	}
}

func TestNoFlushOnStopWhenDisabled(t *testing.T) {
	var flushed atomic.Bool
	tl := tally.New(func(ctx context.Context, batch map[string]int64) {
		flushed.Store(true)
	}, tally.WithFlushInterval(time.Hour), tally.WithFlushOnStop(false))

	tl.Add("k", 1)
	tl.Stop()
	if flushed.Load() {
		t.Fatal("should not flush on stop when disabled")
	}
}

func TestConcurrentAdd(t *testing.T) {
	var total int64
	tl := tally.New(func(ctx context.Context, batch map[string]int64) {
		atomic.AddInt64(&total, batch["hot"])
	}, tally.WithFlushInterval(5*time.Millisecond))

	const goroutines, per = 50, 2000
	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range per {
				tl.Add("hot", 1)
			}
		})
	}
	wg.Wait()
	tl.Stop()

	if atomic.LoadInt64(&total) != goroutines*per {
		t.Fatalf("total = %d, want %d", total, goroutines*per)
	}
}

func TestFloatValues(t *testing.T) {
	var got float64
	var mu sync.Mutex
	tl := tally.New(func(ctx context.Context, batch map[string]float64) {
		mu.Lock()
		got += batch["w"]
		mu.Unlock()
	}, tally.WithFlushInterval(10*time.Millisecond))
	tl.Add("w", 1.5)
	tl.Add("w", 2.25)
	tl.Stop()
	mu.Lock()
	defer mu.Unlock()
	if got != 3.75 {
		t.Fatalf("float sum = %v, want 3.75", got)
	}
}

func TestPanicInFlushRecovered(t *testing.T) {
	var second atomic.Bool
	var n atomic.Int64
	tl := tally.New(func(ctx context.Context, batch map[string]int64) {
		if n.Add(1) == 1 {
			panic("boom in flush")
		}
		second.Store(true)
	}, tally.WithFlushInterval(10*time.Millisecond))

	tl.Add("k", 1)
	time.Sleep(30 * time.Millisecond) // 第一次 flush panic
	tl.Add("k", 1)
	time.Sleep(30 * time.Millisecond) // 第二次 flush 应正常(循环没被 panic 带崩)
	tl.Stop()
	if !second.Load() {
		t.Fatal("flush loop died after panic")
	}
}
