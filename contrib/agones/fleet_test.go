package agones_test

import (
	"context"
	"errors"
	"testing"

	"github.com/rushteam/beauty/contrib/agones"
)

type captureAlloc struct {
	last agones.AllocationRequest
}

func (c *captureAlloc) Allocate(_ context.Context, req agones.AllocationRequest) (agones.AllocationResult, error) {
	c.last = req
	return agones.AllocationResult{GameServerName: "gs-1", Address: "10.0.0.8:7350"}, nil
}

func TestRosterNamespaceAndName(t *testing.T) {
	eu := &captureAlloc{}
	us := &captureAlloc{}
	roster := agones.NewRoster()
	if err := roster.Add(agones.Fleet{Name: "EU", Namespace: "prod-eu", Alloc: eu}); err != nil {
		t.Fatal(err)
	}
	if err := roster.Add(agones.Fleet{Name: "eu", Alloc: us}); !errors.Is(err, agones.ErrFleetExists) {
		t.Fatalf("dup = %v", err)
	}
	if err := roster.Add(agones.Fleet{Name: "US", Alloc: us}); err != nil {
		t.Fatal(err)
	}
	got, ok := roster.Get("eu")
	if !ok || got.Namespace != "prod-eu" || got.Name != "EU" {
		t.Fatalf("get = %+v ok=%v", got, ok)
	}
	usFleet, _ := roster.Get("us")
	if usFleet.Namespace != "default" {
		t.Fatalf("default ns = %q", usFleet.Namespace)
	}

	res, err := roster.Allocate(context.Background(), "Eu", agones.AllocationRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Address != "10.0.0.8:7350" || eu.last.Namespace != "prod-eu" {
		t.Fatalf("result=%+v ns=%q", res, eu.last.Namespace)
	}
	if _, err := roster.Allocate(context.Background(), "EU", agones.AllocationRequest{Namespace: "override"}); err != nil {
		t.Fatal(err)
	}
	if eu.last.Namespace != "override" {
		t.Fatalf("override ns = %q", eu.last.Namespace)
	}
	if _, err := roster.Allocate(context.Background(), "missing", agones.AllocationRequest{}); !errors.Is(err, agones.ErrFleetNotFound) {
		t.Fatalf("missing = %v", err)
	}
	names := roster.Names()
	if len(names) != 2 || names[0] != "EU" || names[1] != "US" {
		t.Fatalf("names = %v", names)
	}
}
