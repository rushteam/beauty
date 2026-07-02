package cooldown_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/cooldown"
)

func TestReadyTriggerRemaining(t *testing.T) {
	c := cooldown.New(50 * time.Millisecond)
	defer c.Stop()

	if !c.Ready("skill") {
		t.Fatal("fresh key should be ready")
	}
	c.Trigger("skill")
	if c.Ready("skill") {
		t.Fatal("should be on cooldown right after trigger")
	}
	if rem := c.Remaining("skill"); rem <= 0 || rem > 50*time.Millisecond {
		t.Fatalf("remaining = %v, want (0, 50ms]", rem)
	}
	time.Sleep(70 * time.Millisecond)
	if !c.Ready("skill") {
		t.Fatal("should be ready after cd elapsed")
	}
	if c.Remaining("skill") != 0 {
		t.Fatal("remaining should be 0 when ready")
	}
}

func TestTryTrigger(t *testing.T) {
	c := cooldown.New(50 * time.Millisecond)
	defer c.Stop()

	if !c.TryTrigger("k") {
		t.Fatal("first TryTrigger should succeed")
	}
	if c.TryTrigger("k") {
		t.Fatal("second TryTrigger during cd should fail")
	}
	time.Sleep(70 * time.Millisecond)
	if !c.TryTrigger("k") {
		t.Fatal("TryTrigger after cd should succeed")
	}
}

func TestTryTrigger_AtomicUnderConcurrency(t *testing.T) {
	// 大量并发 TryTrigger 同一 key,只有一个应成功(CD 期内)。
	c := cooldown.New(time.Hour) // 长 CD,确保只有首个成功
	defer c.Stop()

	var success atomic.Int64
	var wg sync.WaitGroup
	for range 200 {
		wg.Go(func() {
			if c.TryTrigger("skill") {
				success.Add(1)
			}
		})
	}
	wg.Wait()
	if success.Load() != 1 {
		t.Fatalf("exactly one TryTrigger should win, got %d", success.Load())
	}
}

func TestTriggerFor_PerActionCD(t *testing.T) {
	c := cooldown.New(time.Hour) // 默认很长
	defer c.Stop()

	c.TriggerFor("quick", 30*time.Millisecond) // 覆盖为短 CD
	time.Sleep(50 * time.Millisecond)
	if !c.Ready("quick") {
		t.Fatal("per-action short CD should have elapsed")
	}
}

func TestReset(t *testing.T) {
	c := cooldown.New(time.Hour)
	defer c.Stop()
	c.Trigger("k")
	if c.Ready("k") {
		t.Fatal("should be on cd")
	}
	c.Reset("k")
	if !c.Ready("k") {
		t.Fatal("reset should make it ready")
	}
}

func TestZeroCDAlwaysReady(t *testing.T) {
	c := cooldown.New(0)
	defer c.Stop()
	c.Trigger("k")
	if !c.Ready("k") {
		t.Fatal("zero cd should be immediately ready")
	}
	if !c.TryTrigger("k") {
		t.Fatal("zero cd TryTrigger should always succeed")
	}
}

func TestGC_ReclaimsExpired(t *testing.T) {
	c := cooldown.New(20*time.Millisecond, cooldown.WithGCInterval(10*time.Millisecond))
	defer c.Stop()
	for i := range 100 {
		c.Trigger(string(rune('a'+i%26)) + string(rune(i)))
	}
	time.Sleep(80 * time.Millisecond) // 过期 + gc 跑过
	// 无直接 Len API;确认仍能正常用(不 panic,新 key 就绪)。
	if !c.Ready("brand-new") {
		t.Fatal("new key should be ready after gc")
	}
}

func TestConcurrentMixedKeys(t *testing.T) {
	c := cooldown.New(time.Millisecond)
	defer c.Stop()
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Go(func() {
			key := string(rune('a' + i%26))
			for range 1000 {
				c.TryTrigger(key)
				c.Ready(key)
				c.Remaining(key)
			}
		})
	}
	wg.Wait()
}
