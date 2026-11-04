package agent_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rushteam/beauty/contrib/llm/agent"
)

func TestMemoryRunStore_Lease(t *testing.T) {
	store := agent.NewMemoryRunStore()
	ctx := context.Background()
	lease, err := store.Claim(ctx, "r", "one", 40*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(ctx, "r", "two", time.Second); !errors.Is(err, agent.ErrRunClaimed) {
		t.Fatalf("claim error = %v", err)
	}
	renewed, err := store.Renew(ctx, "r", lease, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if renewed.Token != lease.Token || !renewed.ExpiresAt.After(lease.ExpiresAt) {
		t.Fatalf("renewed = %+v, lease = %+v", renewed, lease)
	}
	if err := store.Release(ctx, "r", lease); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(ctx, "r", "two", time.Second); err != nil {
		t.Fatalf("claim after release: %v", err)
	}
}

func TestMemoryRunStore_LeaseExpiryAllowsTakeover(t *testing.T) {
	store := agent.NewMemoryRunStore()
	ctx := context.Background()
	old, err := store.Claim(ctx, "r", "one", time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	next, err := store.Claim(ctx, "r", "two", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Renew(ctx, "r", old, time.Second); !errors.Is(err, agent.ErrLeaseLost) {
		t.Fatalf("old renew = %v", err)
	}
	if err := store.Release(ctx, "r", next); err != nil {
		t.Fatal(err)
	}
}
