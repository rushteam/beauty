package replicate_test

import (
	"testing"

	"github.com/rushteam/beauty/pkg/replicate"
	"github.com/rushteam/beauty/pkg/spatial"
)

func TestProjectorDelta(t *testing.T) {
	p := replicate.NewProjector[string](replicate.Config{})
	lookup := func(id string) (replicate.EntityState, bool) {
		return replicate.EntityState{ID: id, X: 1, Y: 2, Version: 1}, true
	}
	visible := []spatial.Entity[string]{{ID: "a"}, {ID: "b"}}

	d := p.Project(1, "viewer", visible, nil, nil, lookup)
	if !d.Baseline || len(d.Spawn) != 2 {
		t.Fatalf("first frame baseline spawn=%d baseline=%v", len(d.Spawn), d.Baseline)
	}

	d = p.Project(2, "viewer", visible, []string{"a"}, nil, lookup)
	if d.Baseline {
		t.Fatal("expected incremental")
	}
	if len(d.Update) != 1 || d.Update[0].ID != "a" {
		t.Fatalf("update=%v", d.Update)
	}

	d = p.Project(1, "lonely", nil, nil, nil, lookup)
	if !d.Baseline {
		t.Fatal("empty AOI first frame should baseline")
	}
}

func TestDirtySet(t *testing.T) {
	d := replicate.NewDirtySet[string]()
	d.Mark("a")
	d.Remove("b")
	dirty, removed := d.Consume()
	if len(dirty) != 1 || dirty[0] != "a" {
		t.Fatalf("dirty=%v", dirty)
	}
	if len(removed) != 1 || removed[0] != "b" {
		t.Fatalf("removed=%v", removed)
	}
}
