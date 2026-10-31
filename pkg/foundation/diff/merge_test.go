package diff

import (
	"reflect"
	"testing"
)

func TestMerge3OneSideChanged(t *testing.T) {
	base := []int{1, 2, 3, 4, 5}
	a := []int{1, 2, 30, 4, 5} // a 改了第 3 个
	b := []int{1, 2, 3, 4, 5}  // b 未改
	merged, conflicts := Merge3(base, a, b)
	if len(conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %+v", conflicts)
	}
	if !reflect.DeepEqual(merged, a) {
		t.Fatalf("got %v want %v", merged, a)
	}
}

func TestMerge3BothSidesDisjoint(t *testing.T) {
	base := []int{1, 2, 3, 4, 5}
	a := []int{1, 20, 3, 4, 5} // a 改第 2 个
	b := []int{1, 2, 3, 40, 5} // b 改第 4 个
	merged, conflicts := Merge3(base, a, b)
	if len(conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %+v", conflicts)
	}
	want := []int{1, 20, 3, 40, 5}
	if !reflect.DeepEqual(merged, want) {
		t.Fatalf("got %v want %v", merged, want)
	}
}

func TestMerge3SameEdit(t *testing.T) {
	base := []int{1, 2, 3}
	a := []int{1, 9, 3}
	b := []int{1, 9, 3}
	merged, conflicts := Merge3(base, a, b)
	if len(conflicts) != 0 {
		t.Fatalf("same edit must not conflict: %+v", conflicts)
	}
	if !reflect.DeepEqual(merged, a) {
		t.Fatalf("got %v want %v", merged, a)
	}
}

func TestMerge3Conflict(t *testing.T) {
	base := []int{1, 2, 3}
	a := []int{1, 8, 3}
	b := []int{1, 9, 3}
	merged, conflicts := Merge3(base, a, b)
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d (%+v)", len(conflicts), conflicts)
	}
	c := conflicts[0]
	if !reflect.DeepEqual(c.Base, []int{2}) || !reflect.DeepEqual(c.A, []int{8}) || !reflect.DeepEqual(c.B, []int{9}) {
		t.Fatalf("conflict contents: %+v", c)
	}
	if !reflect.DeepEqual(merged, []int{1, 8, 3}) { // 默认取 ours
		t.Fatalf("merged should take ours: %v", merged)
	}
}

func TestMerge3Insertions(t *testing.T) {
	base := []int{1, 2, 3}
	a := []int{1, 2, 3, 4} // a 追加 4
	b := []int{0, 1, 2, 3} // b 头插 0
	merged, conflicts := Merge3(base, a, b)
	if len(conflicts) != 0 {
		t.Fatalf("disjoint insertions should merge: %+v", conflicts)
	}
	want := []int{0, 1, 2, 3, 4}
	if !reflect.DeepEqual(merged, want) {
		t.Fatalf("got %v want %v", merged, want)
	}
}

func TestMerge3Lines(t *testing.T) {
	base := "a\nb\nc"
	a := "a\nB\nc"
	b := "a\nb\nC"
	merged, conflicts := Merge3Lines(base, a, b)
	if len(conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %+v", conflicts)
	}
	if merged != "a\nB\nC" {
		t.Fatalf("got %q", merged)
	}
}
