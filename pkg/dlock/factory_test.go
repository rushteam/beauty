package dlock_test

import (
	"context"
	"net/url"
	"testing"

	"github.com/rushteam/beauty/pkg/dlock"
)

type fakeLock struct{}

func (fakeLock) Unlock(context.Context) error { return nil }

type fakeLocker struct{ dsn string }

func (f fakeLocker) Lock(context.Context, string) (dlock.Lock, error) { return fakeLock{}, nil }
func (f fakeLocker) TryLock(context.Context, string) (dlock.Lock, bool, error) {
	return fakeLock{}, true, nil
}

type fakeElector struct{ dsn string }

func (f fakeElector) Run(context.Context, string, func(context.Context)) error { return nil }

func TestNew_RegistryRoundtrip(t *testing.T) {
	dlock.RegisterLocker("faketest", func(u *url.URL) (dlock.Locker, error) {
		return fakeLocker{dsn: u.Host}, nil
	})
	dlock.RegisterElector("faketest", func(u *url.URL) (dlock.Elector, error) {
		return fakeElector{dsn: u.Host}, nil
	})

	l, err := dlock.New("faketest://example:1234/?prefix=p")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := l.(fakeLocker).dsn; got != "example:1234" {
		t.Fatalf("locker host = %q, want example:1234", got)
	}

	e, err := dlock.NewElector("faketest://example:5678")
	if err != nil {
		t.Fatalf("NewElector: %v", err)
	}
	if got := e.(fakeElector).dsn; got != "example:5678" {
		t.Fatalf("elector host = %q, want example:5678", got)
	}
}

func TestNew_UnsupportedScheme(t *testing.T) {
	if _, err := dlock.New("nope://x"); err == nil {
		t.Fatal("New with unregistered scheme should error")
	}
	if _, err := dlock.NewElector("nope://x"); err == nil {
		t.Fatal("NewElector with unregistered scheme should error")
	}
}

func TestRegisterLocker_DuplicatePanics(t *testing.T) {
	dlock.RegisterLocker("duptest", func(*url.URL) (dlock.Locker, error) { return nil, nil })
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate RegisterLocker should panic")
		}
	}()
	dlock.RegisterLocker("duptest", func(*url.URL) (dlock.Locker, error) { return nil, nil })
}
