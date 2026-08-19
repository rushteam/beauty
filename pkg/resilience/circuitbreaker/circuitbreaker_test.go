package circuitbreaker

import (
	"errors"
	"sync"
	"testing"
	"time"
)

var errTest = errors.New("test error")

func TestBreaker_ClosedToOpen(t *testing.T) {
	var transitions []string
	b := New(
		WithThreshold(0.5),
		WithWindow(10*time.Second),
		WithMinRequests(4),
		WithOnStateChange(func(from, to State) {
			transitions = append(transitions, from.String()+"->"+to.String())
		}),
	)

	if b.State() != StateClosed {
		t.Fatalf("initial state = %v, want closed", b.State())
	}

	// 4 requests: 2 ok, 2 fail => 50% error rate => triggers open
	b.Do(func() error { return nil })
	b.Do(func() error { return nil })
	b.Do(func() error { return errTest })
	b.Do(func() error { return errTest })

	if b.State() != StateOpen {
		t.Fatalf("state = %v, want open", b.State())
	}
	if len(transitions) != 1 || transitions[0] != "closed->open" {
		t.Fatalf("transitions = %v", transitions)
	}
}

func TestBreaker_OpenRejectsRequests(t *testing.T) {
	b := New(WithThreshold(0.1), WithMinRequests(1), WithCooldown(time.Hour))
	b.Do(func() error { return errTest })

	err := b.Do(func() error { return nil })
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("want ErrCircuitOpen, got %v", err)
	}
}

func TestBreaker_HalfOpenToClosedOnSuccess(t *testing.T) {
	var transitions []string
	b := New(
		WithThreshold(0.5),
		WithMinRequests(2),
		WithCooldown(10*time.Millisecond),
		WithHalfOpenMax(2),
		WithOnStateChange(func(from, to State) {
			transitions = append(transitions, from.String()+"->"+to.String())
		}),
	)

	// Trip the breaker
	b.Do(func() error { return errTest })
	b.Do(func() error { return errTest })

	if b.State() != StateOpen {
		t.Fatalf("state = %v, want open", b.State())
	}

	// Wait for cooldown
	time.Sleep(20 * time.Millisecond)

	if b.State() != StateHalfOpen {
		t.Fatalf("state = %v, want half-open", b.State())
	}

	// Successful probes
	b.Do(func() error { return nil })
	b.Do(func() error { return nil })

	if b.State() != StateClosed {
		t.Fatalf("state = %v, want closed", b.State())
	}

	expected := []string{"closed->open", "open->half-open", "half-open->closed"}
	if len(transitions) != len(expected) {
		t.Fatalf("transitions = %v, want %v", transitions, expected)
	}
	for i, e := range expected {
		if transitions[i] != e {
			t.Fatalf("transitions[%d] = %v, want %v", i, transitions[i], e)
		}
	}
}

func TestBreaker_HalfOpenToOpenOnFailure(t *testing.T) {
	b := New(
		WithThreshold(0.5),
		WithMinRequests(2),
		WithCooldown(10*time.Millisecond),
		WithHalfOpenMax(3),
	)

	b.Do(func() error { return errTest })
	b.Do(func() error { return errTest })

	time.Sleep(20 * time.Millisecond)
	if b.State() != StateHalfOpen {
		t.Fatalf("state = %v, want half-open", b.State())
	}

	// One failure in half-open goes back to open
	b.Do(func() error { return errTest })

	if b.State() != StateOpen {
		t.Fatalf("state = %v, want open", b.State())
	}
}

func TestBreaker_WindowReset(t *testing.T) {
	b := New(
		WithThreshold(0.5),
		WithMinRequests(10),
		WithWindow(20*time.Millisecond),
	)

	// Record some failures (not enough to trip)
	for i := 0; i < 5; i++ {
		b.Do(func() error { return errTest })
	}

	// Wait for window to expire
	time.Sleep(30 * time.Millisecond)

	// Now do a request — should be in a fresh window
	b.Do(func() error { return nil })

	if b.State() != StateClosed {
		t.Fatalf("state = %v after window reset, want closed", b.State())
	}
}

func TestBreaker_BelowMinRequests(t *testing.T) {
	b := New(WithThreshold(0.1), WithMinRequests(10))

	// All fail but below minRequests → stays closed
	for i := 0; i < 9; i++ {
		b.Do(func() error { return errTest })
	}

	if b.State() != StateClosed {
		t.Fatalf("state = %v, want closed (below minRequests)", b.State())
	}
}

func TestBreaker_Concurrent(t *testing.T) {
	b := New(
		WithThreshold(0.6),
		WithMinRequests(20),
		WithWindow(5*time.Second),
		WithCooldown(10*time.Millisecond),
		WithHalfOpenMax(5),
	)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			b.Do(func() error {
				if n%3 == 0 {
					return errTest
				}
				return nil
			})
		}(i)
	}
	wg.Wait()

	// Just verify no panics and state is valid
	s := b.State()
	if s != StateClosed && s != StateOpen && s != StateHalfOpen {
		t.Fatalf("invalid state %v", s)
	}
}
