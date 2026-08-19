package quadtree

import (
	"math"
	"math/rand/v2"
	"testing"
)

func TestAddAndLen(t *testing.T) {
	qt := New[int](Rect{0, 0, 100, 100}, 4, 8)
	qt.Add(1, 10, 10)
	qt.Add(2, 50, 50)
	qt.Add(3, 90, 90)
	if qt.Len() != 3 {
		t.Fatalf("Len = %d, want 3", qt.Len())
	}
}

func TestAddDuplicate(t *testing.T) {
	qt := New[int](Rect{0, 0, 100, 100}, 4, 8)
	qt.Add(1, 10, 10)
	qt.Add(1, 50, 50) // move
	if qt.Len() != 1 {
		t.Fatalf("Len = %d after duplicate Add, want 1", qt.Len())
	}
	x, y, ok := qt.Pos(1)
	if !ok || x != 50 || y != 50 {
		t.Errorf("Pos after move = (%v,%v,%v), want (50,50,true)", x, y, ok)
	}
}

func TestRemove(t *testing.T) {
	qt := New[int](Rect{0, 0, 100, 100}, 4, 8)
	qt.Add(1, 10, 10)
	qt.Add(2, 50, 50)
	qt.Remove(1)
	if qt.Len() != 1 {
		t.Fatalf("Len = %d after Remove, want 1", qt.Len())
	}
	_, _, ok := qt.Pos(1)
	if ok {
		t.Error("Pos(1) should be not found after Remove")
	}
}

func TestNearby(t *testing.T) {
	qt := New[int](Rect{0, 0, 100, 100}, 4, 8)
	qt.Add(1, 10, 10)
	qt.Add(2, 12, 12)
	qt.Add(3, 90, 90)

	results := qt.Nearby(10, 10, 5)
	if len(results) != 2 {
		t.Fatalf("Nearby returned %d, want 2", len(results))
	}
	if results[0].ID != 1 {
		t.Errorf("closest should be ID=1, got %d", results[0].ID)
	}
}

func TestNearbyExclude(t *testing.T) {
	qt := New[int](Rect{0, 0, 100, 100}, 4, 8)
	qt.Add(1, 10, 10)
	qt.Add(2, 11, 11)

	results := qt.Nearby(10, 10, 5, 1)
	if len(results) != 1 || results[0].ID != 2 {
		t.Errorf("Nearby with exclude: got %v, want [{ID:2 ...}]", results)
	}
}

func TestKNN(t *testing.T) {
	qt := New[int](Rect{0, 0, 100, 100}, 4, 8)
	for i := range 20 {
		qt.Add(i, float64(i*5), float64(i*5))
	}
	results := qt.KNN(0, 0, 3, 0)
	if len(results) != 3 {
		t.Fatalf("KNN returned %d, want 3", len(results))
	}
	if results[0].ID != 0 {
		t.Errorf("KNN closest should be ID=0, got %d", results[0].ID)
	}
}

func TestQueryRect(t *testing.T) {
	qt := New[int](Rect{0, 0, 100, 100}, 4, 8)
	qt.Add(1, 10, 10)
	qt.Add(2, 50, 50)
	qt.Add(3, 80, 80)

	results := qt.QueryRect(Rect{0, 0, 60, 60})
	if len(results) != 2 {
		t.Fatalf("QueryRect returned %d, want 2", len(results))
	}
	ids := map[int]bool{}
	for _, e := range results {
		ids[e.ID] = true
	}
	if !ids[1] || !ids[2] {
		t.Errorf("QueryRect should contain IDs 1,2; got %v", ids)
	}
}

func TestSubdivision(t *testing.T) {
	qt := New[int](Rect{0, 0, 100, 100}, 2, 8) // capacity=2, triggers split
	qt.Add(1, 10, 10)
	qt.Add(2, 20, 20)
	qt.Add(3, 30, 30) // triggers subdivision
	qt.Add(4, 40, 40)

	results := qt.Nearby(25, 25, 50)
	if len(results) != 4 {
		t.Fatalf("after subdivision, Nearby returned %d, want 4", len(results))
	}
}

func TestCollapse(t *testing.T) {
	qt := New[int](Rect{0, 0, 100, 100}, 2, 8)
	qt.Add(1, 10, 10)
	qt.Add(2, 20, 20)
	qt.Add(3, 30, 30) // triggers split

	qt.Remove(3) // should collapse back
	qt.Remove(2)
	if qt.Len() != 1 {
		t.Errorf("Len after removals = %d, want 1", qt.Len())
	}
	results := qt.Nearby(10, 10, 1)
	if len(results) != 1 || results[0].ID != 1 {
		t.Errorf("after collapse, Nearby(10,10,1) = %v, want [{ID:1}]", results)
	}
}

func TestNearbyDistanceSorted(t *testing.T) {
	qt := New[int](Rect{0, 0, 1000, 1000}, 8, 10)
	rng := rand.New(rand.NewPCG(42, 0))
	for i := range 100 {
		qt.Add(i, rng.Float64()*1000, rng.Float64()*1000)
	}
	results := qt.Nearby(500, 500, 300)
	for i := 1; i < len(results); i++ {
		if results[i].Dist < results[i-1].Dist {
			t.Fatalf("results not sorted at index %d: %v > %v", i, results[i-1].Dist, results[i].Dist)
		}
	}
}

func TestOutOfBounds(t *testing.T) {
	qt := New[int](Rect{0, 0, 100, 100}, 4, 8)
	qt.Add(1, 200, 200) // 超出边界,应被静默忽略
	results := qt.Nearby(200, 200, 10)
	if len(results) != 0 {
		t.Errorf("out-of-bounds entity should not be queryable, got %v", results)
	}
}

func TestNearbyAccuracy(t *testing.T) {
	qt := New[int](Rect{0, 0, 1000, 1000}, 8, 10)
	rng := rand.New(rand.NewPCG(7, 0))
	type pt struct{ x, y float64 }
	pts := make([]pt, 200)
	for i := range pts {
		pts[i] = pt{rng.Float64() * 1000, rng.Float64() * 1000}
		qt.Add(i, pts[i].x, pts[i].y)
	}

	cx, cy, radius := 500.0, 500.0, 150.0
	results := qt.Nearby(cx, cy, radius)
	resultSet := map[int]bool{}
	for _, e := range results {
		resultSet[e.ID] = true
	}

	for i, p := range pts {
		dx, dy := p.x-cx, p.y-cy
		dist := math.Sqrt(dx*dx + dy*dy)
		inRange := dist <= radius
		if inRange && !resultSet[i] {
			t.Errorf("entity %d at (%v,%v) dist=%v should be in results", i, p.x, p.y, dist)
		}
		if !inRange && resultSet[i] {
			t.Errorf("entity %d at (%v,%v) dist=%v should NOT be in results", i, p.x, p.y, dist)
		}
	}
}
