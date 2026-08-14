package agones_test

import (
	"context"
	"testing"

	"github.com/rushteam/beauty/contrib/agones"
)

func TestPoolAllocator(t *testing.T) {
	p := agones.NewPoolAllocator([]string{"a:1", "b:2"})
	r1, err := p.Allocate(context.Background(), agones.AllocationRequest{})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := p.Allocate(context.Background(), agones.AllocationRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if r1.Address == r2.Address {
		t.Fatalf("expected rotation, got %q twice", r1.Address)
	}
}

func TestNewGRPCAllocatorRequiresTLS(t *testing.T) {
	_, err := agones.NewGRPCAllocator("127.0.0.1:443")
	if err == nil {
		t.Fatal("expected error without TLS or insecure")
	}
}
