package syncx_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/syncx"
)

// ---- Map / ForEach ----

func TestMap_OrderAndConcurrency(t *testing.T) {
	items := []int{1, 2, 3, 4, 5, 6, 7, 8}
	var inFlight, maxInFlight atomic.Int64
	got, err := syncx.Map(context.Background(), items, 3, func(_ context.Context, n int) (int, error) {
		cur := inFlight.Add(1)
		for { // 记录峰值并发
			m := maxInFlight.Load()
			if cur <= m || maxInFlight.CompareAndSwap(m, cur) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		inFlight.Add(-1)
		return n * n, nil
	})
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	for i, v := range got { // 结果按输入顺序
		if v != items[i]*items[i] {
			t.Fatalf("got[%d]=%d, want %d", i, v, items[i]*items[i])
		}
	}
	if maxInFlight.Load() > 3 {
		t.Fatalf("并发峰值 %d 超过 limit 3", maxInFlight.Load())
	}
}

func TestMap_ErrorCancels(t *testing.T) {
	var ran atomic.Int64
	_, err := syncx.Map(context.Background(), []int{1, 2, 3, 4, 5}, 2, func(ctx context.Context, n int) (int, error) {
		ran.Add(1)
		if n == 2 {
			return 0, errors.New("boom")
		}
		<-ctx.Done() // 出错后其余应被取消
		return n, ctx.Err()
	})
	if err == nil || err.Error() != "boom" {
		t.Fatalf("应返回首个错误 boom, got %v", err)
	}
}

func TestForEach(t *testing.T) {
	var sum atomic.Int64
	err := syncx.ForEach(context.Background(), []int{1, 2, 3, 4}, 0, func(_ context.Context, n int) error {
		sum.Add(int64(n))
		return nil
	})
	if err != nil || sum.Load() != 10 {
		t.Fatalf("err=%v sum=%d want 10", err, sum.Load())
	}
}

// ---- SingleFlight ----

func TestSingleFlight_Dedup(t *testing.T) {
	var g syncx.Group[int]
	var calls atomic.Int64
	fn := func() (int, error) {
		calls.Add(1)
		time.Sleep(50 * time.Millisecond)
		return 42, nil
	}
	var wg sync.WaitGroup
	var shared atomic.Int64
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err, sh := g.Do("k", fn)
			if err != nil || v != 42 {
				t.Errorf("do: v=%d err=%v", v, err)
			}
			if sh {
				shared.Add(1)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("并发相同 key 应只执行 1 次, 实际 %d", calls.Load())
	}
	if shared.Load() == 0 {
		t.Fatal("应有调用标记为 shared")
	}
}

// ---- Batcher ----

func TestBatcher_FlushBySize(t *testing.T) {
	var mu sync.Mutex
	var batches [][]int
	b := syncx.NewBatcher(3, time.Hour, func(items []int) { // maxWait 很大 → 只按 size flush
		mu.Lock()
		batches = append(batches, append([]int(nil), items...))
		mu.Unlock()
	})
	for i := 1; i <= 7; i++ {
		b.Add(i)
	}
	b.Close() // flush 剩余
	mu.Lock()
	defer mu.Unlock()
	// 期望:满 3 的批 [1,2,3] [4,5,6],Close 时剩 [7]。
	if len(batches) != 3 {
		t.Fatalf("批数 = %d, want 3: %v", len(batches), batches)
	}
	if len(batches[0]) != 3 || len(batches[2]) != 1 {
		t.Fatalf("批大小异常: %v", batches)
	}
}

func TestBatcher_FlushByTime(t *testing.T) {
	got := make(chan []int, 4)
	b := syncx.NewBatcher(100, 50*time.Millisecond, func(items []int) {
		got <- append([]int(nil), items...)
	})
	defer b.Close()
	b.Add(1)
	b.Add(2)
	select {
	case batch := <-got:
		if len(batch) != 2 {
			t.Fatalf("按时间 flush 应含 2 条, got %v", batch)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("超时:未按时间 flush")
	}
}

// ---- Debounce / Throttle ----

func TestDebounce(t *testing.T) {
	var fired atomic.Int64
	call, _ := syncx.Debounce(60*time.Millisecond, func() { fired.Add(1) })
	for range 5 { // 连续快速调用
		call()
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(120 * time.Millisecond) // 静默后应只触发一次
	if fired.Load() != 1 {
		t.Fatalf("去抖后应触发 1 次, 实际 %d", fired.Load())
	}
}

func TestThrottle(t *testing.T) {
	var fired atomic.Int64
	call := syncx.Throttle(60*time.Millisecond, func() { fired.Add(1) })
	for range 5 { // 60ms 内连打 → 只前沿一次
		call()
		time.Sleep(5 * time.Millisecond)
	}
	if fired.Load() != 1 {
		t.Fatalf("限频窗口内应只触发 1 次, 实际 %d", fired.Load())
	}
	time.Sleep(70 * time.Millisecond)
	call() // 新窗口
	if fired.Load() != 2 {
		t.Fatalf("新窗口应再触发, 实际 %d", fired.Load())
	}
}

// ---- Future ----

func TestFuture(t *testing.T) {
	f := syncx.Async(func() (string, error) {
		time.Sleep(20 * time.Millisecond)
		return "ok", nil
	})
	v, err := f.Await(context.Background())
	if err != nil || v != "ok" {
		t.Fatalf("await: v=%q err=%v", v, err)
	}

	// panic → error
	fp := syncx.Async(func() (int, error) { panic("boom") })
	if _, err := fp.Await(context.Background()); err == nil {
		t.Fatal("panic 应转为 error")
	}

	// ctx 取消
	fs := syncx.Async(func() (int, error) { time.Sleep(time.Second); return 1, nil })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := fs.Await(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ctx 超时应返回 DeadlineExceeded, got %v", err)
	}
}
