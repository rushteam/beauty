package safe

import (
	"errors"
	"testing"
)

func TestRun_RecoversPanic(t *testing.T) {
	err := Run(func() error { panic("boom") })
	if err == nil {
		t.Fatal("want error from panic")
	}
	var pe *PanicError
	if !errors.As(err, &pe) {
		t.Fatalf("want *PanicError, got %T", err)
	}
	if len(pe.Stack) == 0 {
		t.Error("stack should be captured")
	}
}

func TestRun_PassesError(t *testing.T) {
	sentinel := errors.New("x")
	if err := Run(func() error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel, got %v", err)
	}
}

func TestPanicError_UnwrapsErrorValue(t *testing.T) {
	sentinel := errors.New("inner")
	err := Run(func() error { panic(sentinel) })
	if !errors.Is(err, sentinel) {
		t.Fatalf("panic(error) should unwrap to the error: %v", err)
	}
}

func TestGo_RecoversAndReports(t *testing.T) {
	done := make(chan error, 1)
	Go(func() { panic("async boom") }, func(err error) { done <- err })
	err := <-done
	var pe *PanicError
	if !errors.As(err, &pe) {
		t.Fatalf("want *PanicError, got %v", err)
	}
}
