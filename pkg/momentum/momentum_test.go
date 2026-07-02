package momentum_test

import (
	"math"
	"sync"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/momentum"
)

func TestCombo_IncrementsWithinWindow(t *testing.T) {
	tr := momentum.New(momentum.WithComboWindow(time.Hour))
	for i := 1; i <= 5; i++ {
		st := tr.Hit("k", 1)
		if st.Combo != i {
			t.Fatalf("hit %d: combo = %d, want %d", i, st.Combo, i)
		}
	}
	if tr.State("k").MaxCombo != 5 {
		t.Fatalf("maxCombo = %d, want 5", tr.State("k").MaxCombo)
	}
}

func TestCombo_BreaksAfterWindow(t *testing.T) {
	tr := momentum.New(momentum.WithComboWindow(20 * time.Millisecond))
	tr.Hit("k", 1)
	tr.Hit("k", 1) // combo 2
	if c := tr.Combo("k"); c != 2 {
		t.Fatalf("combo = %d, want 2", c)
	}
	time.Sleep(40 * time.Millisecond) // 超过窗口
	if c := tr.Combo("k"); c != 0 {
		t.Fatalf("combo after window = %d, want 0 (broken)", c)
	}
	// 下次 Hit 从 1 重开,但 maxCombo 保留
	st := tr.Hit("k", 1)
	if st.Combo != 1 {
		t.Fatalf("combo after break = %d, want 1", st.Combo)
	}
	if st.MaxCombo != 2 {
		t.Fatalf("maxCombo = %d, want 2 (preserved)", st.MaxCombo)
	}
}

func TestValue_Accumulates(t *testing.T) {
	tr := momentum.New(momentum.WithHalfLife(time.Hour)) // 衰减极慢,近似不衰减
	tr.Hit("k", 10)
	tr.Hit("k", 5)
	if v := tr.Value("k"); math.Abs(v-15) > 0.1 {
		t.Fatalf("value = %v, want ~15", v)
	}
}

func TestValue_DecaysOverHalfLife(t *testing.T) {
	tr := momentum.New(momentum.WithHalfLife(50 * time.Millisecond))
	tr.Hit("k", 100)
	time.Sleep(50 * time.Millisecond) // 一个半衰期
	v := tr.Value("k")
	// 应衰减到 ~50(允许调度误差)。
	if v < 35 || v > 65 {
		t.Fatalf("after one half-life value = %v, want ~50", v)
	}
}

func TestValue_DecaysTowardZero(t *testing.T) {
	tr := momentum.New(momentum.WithHalfLife(20 * time.Millisecond))
	tr.Hit("k", 100)
	time.Sleep(200 * time.Millisecond) // 多个半衰期
	if v := tr.Value("k"); v > 5 {
		t.Fatalf("after many half-lives value = %v, want near 0", v)
	}
}

func TestReset(t *testing.T) {
	tr := momentum.New()
	tr.Hit("k", 50)
	tr.Reset("k")
	if st := tr.State("k"); st.Combo != 0 || st.Value != 0 || st.MaxCombo != 0 {
		t.Fatalf("after reset = %+v, want zero", st)
	}
}

func TestState_UnknownKey(t *testing.T) {
	tr := momentum.New()
	if st := tr.State("nope"); st.Combo != 0 || st.Value != 0 {
		t.Fatalf("unknown key = %+v", st)
	}
}

func TestGC_ReclaimsCold(t *testing.T) {
	tr := momentum.New(momentum.WithHalfLife(10*time.Millisecond), momentum.WithComboWindow(10*time.Millisecond))
	for i := range 100 {
		tr.Hit(string(rune('a'+i%26))+string(rune(i)), 1)
	}
	if tr.Len() == 0 {
		t.Fatal("should have keys before GC")
	}
	time.Sleep(150 * time.Millisecond) // 让热度冷却、连击断开
	removed := tr.GC(1e-3)
	if removed == 0 || tr.Len() != 0 {
		t.Fatalf("GC removed %d, remaining %d, want all reclaimed", removed, tr.Len())
	}
}

func TestGC_KeepsHot(t *testing.T) {
	tr := momentum.New(momentum.WithHalfLife(time.Hour))
	tr.Hit("hot", 100)
	if n := tr.GC(1e-3); n != 0 {
		t.Fatalf("GC should keep hot key, removed %d", n)
	}
	if tr.Len() != 1 {
		t.Fatalf("hot key should survive, Len = %d", tr.Len())
	}
}

func TestConcurrent(t *testing.T) {
	tr := momentum.New(momentum.WithHalfLife(time.Hour))
	const goroutines, per = 50, 1000
	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range per {
				tr.Hit("hot", 1)
			}
		})
	}
	wg.Wait()
	// 半衰期极长,总热度应接近总次数。
	if v := tr.Value("hot"); v < float64(goroutines*per)*0.99 {
		t.Fatalf("concurrent value = %v, want ~%d", v, goroutines*per)
	}
}
