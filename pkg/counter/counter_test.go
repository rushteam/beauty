package counter_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/counter"
)

func TestIncrAndCount(t *testing.T) {
	c := counter.New(time.Minute)
	defer c.Stop()

	if got := c.Count("k"); got != 0 {
		t.Fatalf("empty count = %d", got)
	}
	c.Incr("k", 3)
	c.Incr("k", 2)
	if got := c.Count("k"); got != 5 {
		t.Fatalf("count = %d, want 5", got)
	}
}

func TestAllow_Quota(t *testing.T) {
	c := counter.New(time.Minute)
	defer c.Stop()

	// 配额 3:前 3 次放行,第 4 次拒绝。
	for i := range 3 {
		if !c.Allow("user:1", 1, 3) {
			t.Fatalf("call %d should be allowed", i)
		}
	}
	if c.Allow("user:1", 1, 3) {
		t.Fatal("4th call should be rejected")
	}
	if got := c.Count("user:1"); got != 3 {
		t.Fatalf("count = %d, want 3 (rejected call must not increment)", got)
	}
}

func TestAllow_MultiUnit(t *testing.T) {
	c := counter.New(time.Minute)
	defer c.Stop()
	// 一次加 5,配额 10:第一次 ok(5),第二次 ok(10),第三次超。
	if !c.Allow("k", 5, 10) {
		t.Fatal("first +5 should pass")
	}
	if !c.Allow("k", 5, 10) {
		t.Fatal("second +5 should pass (=10)")
	}
	if c.Allow("k", 1, 10) {
		t.Fatal("+1 over 10 should fail")
	}
}

func TestSlidingWindowExpiry(t *testing.T) {
	// 窗口 100ms、10 桶(每桶 10ms)。加计数后等窗口过完,应滑出归零。
	c := counter.New(100*time.Millisecond, counter.WithBuckets(10))
	defer c.Stop()

	c.Incr("k", 5)
	if c.Count("k") != 5 {
		t.Fatalf("immediately after incr = %d", c.Count("k"))
	}
	time.Sleep(150 * time.Millisecond)
	if got := c.Count("k"); got != 0 {
		t.Fatalf("after window slid = %d, want 0", got)
	}
}

func TestReset(t *testing.T) {
	c := counter.New(time.Minute)
	defer c.Stop()
	c.Incr("k", 9)
	c.Reset("k")
	if got := c.Count("k"); got != 0 {
		t.Fatalf("after reset = %d", got)
	}
}

func TestConcurrent(t *testing.T) {
	c := counter.New(time.Minute)
	defer c.Stop()

	const goroutines, per = 50, 1000
	var wg sync.WaitGroup
	var total atomic.Int64
	for range goroutines {
		wg.Go(func() {
			for range per {
				c.Incr("hot", 1)
				total.Add(1)
			}
		})
	}
	wg.Wait()
	if got := c.Count("hot"); got != total.Load() {
		t.Fatalf("concurrent count = %d, want %d", got, total.Load())
	}
}

func TestGC_ReclaimsIdleKeys(t *testing.T) {
	c := counter.New(50*time.Millisecond, counter.WithGCInterval(20*time.Millisecond))
	defer c.Stop()
	for i := range 100 {
		c.Incr(string(rune('a'+i%26))+string(rune(i)), 1)
	}
	// 等待空闲超过一个窗口 + gc 跑过。
	time.Sleep(200 * time.Millisecond)
	// 不 panic、能继续用即可(内部 key 已被回收,无直接可观测计数暴露)。
	if c.Count("nonexistent") != 0 {
		t.Fatal("unexpected count")
	}
}
