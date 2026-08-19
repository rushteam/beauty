package timeout

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDo_Success(t *testing.T) {
	err := Do(context.Background(), time.Second, func(ctx context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatalf("Do error: %v", err)
	}
}

func TestDo_PropagatesError(t *testing.T) {
	want := errors.New("business error")
	err := Do(context.Background(), time.Second, func(ctx context.Context) error {
		return want
	})
	if err != want {
		t.Fatalf("got %v, want %v", err, want)
	}
}

func TestDo_Timeout(t *testing.T) {
	err := Do(context.Background(), 20*time.Millisecond, func(ctx context.Context) error {
		time.Sleep(time.Second)
		return nil
	})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("got %v, want ErrTimeout", err)
	}
}

func TestDo_PanicRecovery(t *testing.T) {
	err := Do(context.Background(), time.Second, func(ctx context.Context) error {
		panic("oops")
	})
	if !errors.Is(err, ErrPanic) {
		t.Fatalf("got %v, want PanicError", err)
	}
	var pe *PanicError
	if !errors.As(err, &pe) {
		t.Fatal("cannot As to *PanicError")
	}
	if pe.Value != "oops" {
		t.Fatalf("panic value = %v, want 'oops'", pe.Value)
	}
}

func TestDo_ContextAlreadyCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Do(ctx, time.Second, func(ctx context.Context) error {
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

func TestDo_FnRespectsContext(t *testing.T) {
	err := Do(context.Background(), 30*time.Millisecond, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	// fn respects ctx, returns DeadlineExceeded; Do translates to ErrTimeout
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("got %v, want ErrTimeout", err)
	}
}

func TestDoValue_Success(t *testing.T) {
	val, err := DoValue(context.Background(), time.Second, func(ctx context.Context) (int, error) {
		return 42, nil
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if val != 42 {
		t.Fatalf("val = %d, want 42", val)
	}
}

func TestDoValue_Timeout(t *testing.T) {
	val, err := DoValue(context.Background(), 20*time.Millisecond, func(ctx context.Context) (string, error) {
		time.Sleep(time.Second)
		return "hello", nil
	})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("got %v, want ErrTimeout", err)
	}
	if val != "" {
		t.Fatalf("val = %q, want empty on timeout", val)
	}
}

func TestDoValue_Panic(t *testing.T) {
	_, err := DoValue(context.Background(), time.Second, func(ctx context.Context) (int, error) {
		panic(123)
	})
	if !errors.Is(err, ErrPanic) {
		t.Fatalf("got %v, want PanicError", err)
	}
	var pe *PanicError
	errors.As(err, &pe)
	if pe.Value != 123 {
		t.Fatalf("panic value = %v, want 123", pe.Value)
	}
}

func TestDoValue_FastReturn(t *testing.T) {
	start := time.Now()
	_, err := DoValue(context.Background(), 5*time.Second, func(ctx context.Context) (int, error) {
		return 1, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("Did not return immediately on success")
	}
}
