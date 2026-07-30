package energy

import (
	"sync"
	"testing"
	"time"
)

func TestNew_DefaultFull(t *testing.T) {
	e := New()
	if e.Current() != 100 {
		t.Fatalf("initial current = %d, want 100", e.Current())
	}
	if e.Cap() != 100 {
		t.Fatalf("cap = %d, want 100", e.Cap())
	}
}

func TestSpend_Success(t *testing.T) {
	e := New(WithCap(50))
	if err := e.Spend(30); err != nil {
		t.Fatalf("Spend(30) error: %v", err)
	}
	if e.Current() != 20 {
		t.Fatalf("current = %d, want 20", e.Current())
	}
}

func TestSpend_Insufficient(t *testing.T) {
	e := New(WithCap(10))
	if err := e.Spend(11); err != ErrInsufficient {
		t.Fatalf("Spend(11) = %v, want ErrInsufficient", err)
	}
	if e.Current() != 10 {
		t.Fatalf("current changed after failed spend: %d", e.Current())
	}
}

func TestSpend_Zero(t *testing.T) {
	e := New(WithCap(10))
	if err := e.Spend(0); err != nil {
		t.Fatalf("Spend(0) error: %v", err)
	}
	if e.Current() != 10 {
		t.Fatalf("current = %d, want 10", e.Current())
	}
}

func TestAdd_NoOverflow(t *testing.T) {
	e := New(WithCap(10))
	e.Spend(5)
	got := e.Add(20)
	if got != 10 {
		t.Fatalf("Add(20) = %d, want 10 (cap)", got)
	}
}

func TestAdd_Overflow(t *testing.T) {
	e := New(WithCap(10), WithOverflow(true))
	got := e.Add(5)
	if got != 15 {
		t.Fatalf("Add(5) with overflow = %d, want 15", got)
	}
}

func TestRegen(t *testing.T) {
	base := time.Now()
	e := NewWithState(5, base, WithCap(10), WithRegenInterval(time.Minute), WithRegenAmount(2))

	// Simulate 3 minutes passing: 3 ticks * 2 = 6 gained, 5+6=11 → capped at 10
	e.mu.Lock()
	e.regen(base.Add(3 * time.Minute))
	cur := e.cur
	e.mu.Unlock()

	if cur != 10 {
		t.Fatalf("after 3min regen, cur = %d, want 10", cur)
	}
}

func TestRegen_PartialInterval(t *testing.T) {
	base := time.Now()
	e := NewWithState(5, base, WithCap(100), WithRegenInterval(time.Minute), WithRegenAmount(1))

	// 90s = 1 full interval
	e.mu.Lock()
	e.regen(base.Add(90 * time.Second))
	cur := e.cur
	e.mu.Unlock()

	if cur != 6 {
		t.Fatalf("after 90s regen, cur = %d, want 6", cur)
	}
}

func TestRegen_AlreadyFull(t *testing.T) {
	base := time.Now()
	e := NewWithState(10, base, WithCap(10), WithRegenInterval(time.Minute), WithRegenAmount(1))

	e.mu.Lock()
	e.regen(base.Add(time.Hour))
	cur := e.cur
	e.mu.Unlock()

	if cur != 10 {
		t.Fatalf("full energy after regen = %d, want 10", cur)
	}
}

func TestTimeToFull(t *testing.T) {
	base := time.Now()
	e := NewWithState(7, base, WithCap(10), WithRegenInterval(time.Minute), WithRegenAmount(1))

	got := e.TimeToFull()
	want := 3 * time.Minute
	if got != want {
		t.Fatalf("TimeToFull = %v, want %v", got, want)
	}
}

func TestTimeToFull_AlreadyFull(t *testing.T) {
	e := New(WithCap(10))
	if d := e.TimeToFull(); d != 0 {
		t.Fatalf("TimeToFull when full = %v, want 0", d)
	}
}

func TestTimeToAmount(t *testing.T) {
	base := time.Now()
	e := NewWithState(3, base, WithCap(100), WithRegenInterval(5*time.Minute), WithRegenAmount(2))

	// Need 5 more to reach 8: ceil(5/2)=3 intervals = 15 min
	got := e.TimeToAmount(8)
	want := 15 * time.Minute
	if got != want {
		t.Fatalf("TimeToAmount(8) = %v, want %v", got, want)
	}
}

func TestSnapshot(t *testing.T) {
	e := New(WithCap(50))
	e.Spend(10)
	cur, ts := e.Snapshot()
	if cur != 40 {
		t.Fatalf("snapshot cur = %d, want 40", cur)
	}
	if ts.IsZero() {
		t.Fatal("snapshot lastUpdate is zero")
	}
}

func TestConcurrent(t *testing.T) {
	e := New(WithCap(1000), WithOverflow(true))
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			e.Spend(1)
		}()
		go func() {
			defer wg.Done()
			e.Add(1)
		}()
	}
	wg.Wait()
	// No race/panic
	_ = e.Current()
}
