package aoi_test

import (
	"testing"

	"github.com/rushteam/beauty/pkg/game/spatial"
	"github.com/rushteam/beauty/pkg/game/spatial/aoi"
)

func TestSetDiff(t *testing.T) {
	s := aoi.NewSet[string]()
	ent := func(ids ...string) []spatial.Entity[string] {
		out := make([]spatial.Entity[string], len(ids))
		for i, id := range ids {
			out[i] = spatial.Entity[string]{ID: id}
		}
		return out
	}

	enter, leave, stay := s.Diff(ent("a", "b"))
	if len(enter) != 2 || len(leave) != 0 || len(stay) != 0 {
		t.Fatalf("first diff: enter=%v leave=%v stay=%v", enter, leave, stay)
	}
	s.Update(ent("a", "b"))

	enter, leave, stay = s.Diff(ent("b", "c"))
	if len(enter) != 1 || enter[0] != "c" {
		t.Fatalf("enter want c, got %v", enter)
	}
	if len(leave) != 1 || leave[0] != "a" {
		t.Fatalf("leave want a, got %v", leave)
	}
	if len(stay) != 1 || stay[0] != "b" {
		t.Fatalf("stay want b, got %v", stay)
	}
}
