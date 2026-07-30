package signin

import (
	"sync"
	"testing"
	"time"
)

var loc = time.UTC

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 12, 0, 0, 0, loc)
}

func TestSignIn_Basic(t *testing.T) {
	r := New()
	_, err := r.SignIn(date(2026, 7, 1))
	if err != nil {
		t.Fatal(err)
	}
	if r.Streak() != 1 {
		t.Fatalf("streak = %d, want 1", r.Streak())
	}
	if !r.IsSigned(2026, 7, 1) {
		t.Fatal("day 1 not signed")
	}
}

func TestSignIn_Consecutive(t *testing.T) {
	r := New()
	r.SignIn(date(2026, 7, 1))
	r.SignIn(date(2026, 7, 2))
	r.SignIn(date(2026, 7, 3))

	if r.Streak() != 3 {
		t.Fatalf("streak = %d, want 3", r.Streak())
	}
}

func TestSignIn_BreakStreak(t *testing.T) {
	r := New()
	r.SignIn(date(2026, 7, 1))
	r.SignIn(date(2026, 7, 2))
	// skip day 3
	r.SignIn(date(2026, 7, 4))

	if r.Streak() != 1 {
		t.Fatalf("streak = %d, want 1 (broken)", r.Streak())
	}
}

func TestSignIn_Idempotent(t *testing.T) {
	r := New()
	r.SignIn(date(2026, 7, 1))
	_, err := r.SignIn(date(2026, 7, 1))
	if err != ErrAlreadySigned {
		t.Fatalf("repeat sign = %v, want ErrAlreadySigned", err)
	}
}

func TestSignedDays(t *testing.T) {
	r := New()
	r.SignIn(date(2026, 7, 1))
	r.SignIn(date(2026, 7, 5))
	r.SignIn(date(2026, 7, 20))

	if got := r.SignedDays(2026, 7); got != 3 {
		t.Fatalf("SignedDays = %d, want 3", got)
	}
	if got := r.SignedDays(2026, 6); got != 0 {
		t.Fatalf("SignedDays for June = %d, want 0", got)
	}
}

func TestMonthBitmap(t *testing.T) {
	r := New()
	r.SignIn(date(2026, 7, 1))
	r.SignIn(date(2026, 7, 3))

	bm := r.MonthBitmap(2026, 7)
	// bit0 (day1) + bit2 (day3) = 0b101 = 5
	if bm != 5 {
		t.Fatalf("bitmap = %b, want 101", bm)
	}
}

func TestRetroSign_Success(t *testing.T) {
	r := New(WithRetroMax(2))
	// Sign day 3 first
	r.SignIn(date(2026, 7, 3))
	// Retro-sign day 1 and 2
	_, err := r.RetroSign(date(2026, 7, 3), 2026, 7, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.RetroSign(date(2026, 7, 3), 2026, 7, 2)
	if err != nil {
		t.Fatal(err)
	}

	if !r.IsSigned(2026, 7, 1) || !r.IsSigned(2026, 7, 2) {
		t.Fatal("retro days not signed")
	}
	// After retro filling the gap, streak should be 3
	if r.Streak() != 3 {
		t.Fatalf("streak after retro = %d, want 3", r.Streak())
	}
}

func TestRetroSign_LimitReached(t *testing.T) {
	r := New(WithRetroMax(1))
	r.SignIn(date(2026, 7, 5))
	r.RetroSign(date(2026, 7, 5), 2026, 7, 1)

	_, err := r.RetroSign(date(2026, 7, 5), 2026, 7, 2)
	if err != ErrRetroLimit {
		t.Fatalf("got %v, want ErrRetroLimit", err)
	}
}

func TestRetroSign_FutureDate(t *testing.T) {
	r := New()
	_, err := r.RetroSign(date(2026, 7, 5), 2026, 7, 10)
	if err != ErrRetroFuture {
		t.Fatalf("got %v, want ErrRetroFuture", err)
	}
}

func TestRetroSign_AlreadySigned(t *testing.T) {
	r := New()
	r.SignIn(date(2026, 7, 5))
	_, err := r.RetroSign(date(2026, 7, 5), 2026, 7, 5)
	if err != ErrRetroAlready {
		t.Fatalf("got %v, want ErrRetroAlready", err)
	}
}

func TestRetroSign_InvalidDay(t *testing.T) {
	r := New()
	_, err := r.RetroSign(date(2026, 7, 5), 2026, 7, 32)
	if err != ErrInvalidDay {
		t.Fatalf("got %v, want ErrInvalidDay", err)
	}
}

func TestRetroRemaining(t *testing.T) {
	r := New(WithRetroMax(3))
	now := date(2026, 7, 10)
	if r.RetroRemaining(now) != 3 {
		t.Fatalf("remaining = %d, want 3", r.RetroRemaining(now))
	}
	r.RetroSign(now, 2026, 7, 1)
	if r.RetroRemaining(now) != 2 {
		t.Fatalf("remaining = %d, want 2", r.RetroRemaining(now))
	}
}

func TestRewardFunc(t *testing.T) {
	var called bool
	r := New(WithRewardFunc(func(day int, streak int) []Reward {
		called = true
		return []Reward{{Type: "coin", Amount: streak * 10}}
	}))

	rewards, _ := r.SignIn(date(2026, 7, 1))
	if !called {
		t.Fatal("reward func not called")
	}
	if len(rewards) != 1 || rewards[0].Amount != 10 {
		t.Fatalf("rewards = %v", rewards)
	}
}

func TestCrossMonth(t *testing.T) {
	r := New()
	r.SignIn(date(2026, 6, 30))
	r.SignIn(date(2026, 7, 1))

	if r.Streak() != 2 {
		t.Fatalf("cross-month streak = %d, want 2", r.Streak())
	}
	if r.SignedDays(2026, 6) != 1 {
		t.Fatalf("June signed = %d, want 1", r.SignedDays(2026, 6))
	}
	if r.SignedDays(2026, 7) != 1 {
		t.Fatalf("July signed = %d, want 1", r.SignedDays(2026, 7))
	}
}

func TestConcurrent(t *testing.T) {
	r := New(WithRetroMax(100))
	var wg sync.WaitGroup
	base := date(2026, 7, 15)

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(d int) {
			defer wg.Done()
			if d == 15 {
				r.SignIn(base)
			} else if d >= 1 && d <= 14 {
				r.RetroSign(base, 2026, 7, d)
			}
		}(i%16 + 1)
	}
	wg.Wait()
	// No panics
	_ = r.Streak()
}
