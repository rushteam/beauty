package counter_test

import (
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/counter"
	"github.com/rushteam/beauty/pkg/kvstore"
)

func TestStore_IncrAndCount(t *testing.T) {
	st := kvstore.NewMemory()
	defer st.Stop()
	c := counter.New(time.Minute, counter.WithStore(st))
	defer c.Stop()

	c.Incr("k", 3)
	c.Incr("k", 2)
	if got := c.Count("k"); got != 5 {
		t.Fatalf("store count = %d, want 5", got)
	}
}

func TestStore_AllowQuota(t *testing.T) {
	st := kvstore.NewMemory()
	defer st.Stop()
	c := counter.New(time.Minute, counter.WithStore(st))
	defer c.Stop()

	for i := range 3 {
		if !c.Allow("u1", 1, 3) {
			t.Fatalf("call %d should pass", i)
		}
	}
	if c.Allow("u1", 1, 3) {
		t.Fatal("4th over quota should fail")
	}
	// 关键:超限的那次已回退,计数应精确停在 3。
	if got := c.Count("u1"); got != 3 {
		t.Fatalf("count = %d, want 3 (rejected rolled back)", got)
	}
}

func TestStore_FixedWindowRollover(t *testing.T) {
	st := kvstore.NewMemory()
	defer st.Stop()
	// 50ms 窗口,便于观察固定窗口滚动清零。
	c := counter.New(50*time.Millisecond, counter.WithStore(st))
	defer c.Stop()
	c.Incr("k", 5)
	if c.Count("k") != 5 {
		t.Fatal("in-window count wrong")
	}
	time.Sleep(80 * time.Millisecond) // 跨到新窗口
	if got := c.Count("k"); got != 0 {
		t.Fatalf("after window rollover = %d, want 0", got)
	}
}

func TestStore_Reset(t *testing.T) {
	st := kvstore.NewMemory()
	defer st.Stop()
	c := counter.New(time.Minute, counter.WithStore(st))
	defer c.Stop()
	c.Incr("k", 9)
	c.Reset("k")
	if got := c.Count("k"); got != 0 {
		t.Fatalf("after reset = %d", got)
	}
}

// 两个 Counter 实例共享同一个 store,模拟多实例部署——计数应聚合一致。
func TestStore_SharedAcrossInstances(t *testing.T) {
	st := kvstore.NewMemory()
	defer st.Stop()
	c1 := counter.New(time.Minute, counter.WithStore(st))
	defer c1.Stop()
	c2 := counter.New(time.Minute, counter.WithStore(st))
	defer c2.Stop()

	c1.Incr("shared", 4)
	c2.Incr("shared", 6)
	// 任一实例读到的都是全局累计。
	if got := c1.Count("shared"); got != 10 {
		t.Fatalf("c1 sees %d, want 10 (cross-instance)", got)
	}
	if got := c2.Count("shared"); got != 10 {
		t.Fatalf("c2 sees %d, want 10 (cross-instance)", got)
	}
}
