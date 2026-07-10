package cooldown_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/cooldown"
	"github.com/rushteam/beauty/pkg/kvstore"
)

func TestStore_ReadyTriggerRemaining(t *testing.T) {
	st := kvstore.NewMemory()
	defer st.Stop()
	c := cooldown.New(50*time.Millisecond, cooldown.WithStore(st))
	defer c.Stop()

	if !c.Ready("skill") {
		t.Fatal("fresh should be ready")
	}
	c.Trigger("skill")
	if c.Ready("skill") {
		t.Fatal("should be on cd after trigger")
	}
	if rem := c.Remaining("skill"); rem <= 0 || rem > 60*time.Millisecond {
		t.Fatalf("remaining = %v", rem)
	}
	time.Sleep(70 * time.Millisecond)
	if !c.Ready("skill") {
		t.Fatal("should be ready after cd (store TTL expired)")
	}
}

func TestStore_TryTriggerAtomic(t *testing.T) {
	st := kvstore.NewMemory()
	defer st.Stop()
	c := cooldown.New(time.Hour, cooldown.WithStore(st))
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
		t.Fatalf("exactly one TryTrigger should win (SetNX), got %d", success.Load())
	}
}

func TestStore_Reset(t *testing.T) {
	st := kvstore.NewMemory()
	defer st.Stop()
	c := cooldown.New(time.Hour, cooldown.WithStore(st))
	defer c.Stop()
	c.Trigger("k")
	c.Reset("k")
	if !c.Ready("k") {
		t.Fatal("reset should make ready")
	}
}

// 两个实例共享 store:一个触发冷却,另一个应看到"冷却中"。
func TestStore_SharedAcrossInstances(t *testing.T) {
	st := kvstore.NewMemory()
	defer st.Stop()
	c1 := cooldown.New(time.Hour, cooldown.WithStore(st))
	defer c1.Stop()
	c2 := cooldown.New(time.Hour, cooldown.WithStore(st))
	defer c2.Stop()

	if !c1.TryTrigger("player:skill") {
		t.Fatal("c1 first trigger should win")
	}
	// c2(另一实例)看到冷却中,不能再触发。
	if c2.TryTrigger("player:skill") {
		t.Fatal("c2 should see cooldown from c1 (cross-instance)")
	}
	if c2.Ready("player:skill") {
		t.Fatal("c2 should see not-ready")
	}
}
