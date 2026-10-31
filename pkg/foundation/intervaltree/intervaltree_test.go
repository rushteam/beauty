package intervaltree

import (
	"math/rand"
	"sort"
	"testing"
)

func sortedBounds[V any](ms []Match[V]) [][2]int64 {
	out := make([][2]int64, len(ms))
	for i, m := range ms {
		out[i] = [2]int64{m.Low, m.High}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}

func TestStabAndOverlap(t *testing.T) {
	tr := New[string]()
	tr.Insert(1, 5, "a")
	tr.Insert(3, 8, "b")
	tr.Insert(10, 12, "c")
	tr.Insert(6, 7, "d")

	if got := sortedBounds(tr.Stab(4)); len(got) != 2 {
		t.Fatalf("stab(4)=%v", got)
	}
	if got := tr.Stab(9); len(got) != 0 {
		t.Fatalf("stab(9)=%v", got)
	}
	if got := sortedBounds(tr.Overlap(4, 6)); len(got) != 3 { // a,b,d
		t.Fatalf("overlap(4,6)=%v", got)
	}
	if tr.Any(9, 9) {
		t.Fatal("no interval covers 9")
	}
	if !tr.Any(11, 20) {
		t.Fatal("c overlaps [11,20]")
	}
}

func TestRemove(t *testing.T) {
	tr := New[int]()
	tr.Insert(1, 5, 1)
	tr.Insert(1, 5, 2) // 重复区间
	tr.Insert(2, 9, 3)
	if tr.Len() != 3 {
		t.Fatalf("len=%d", tr.Len())
	}
	if !tr.Remove(1, 5) || tr.Len() != 2 {
		t.Fatal("remove one of duplicates")
	}
	if !tr.Remove(1, 5) || tr.Len() != 1 {
		t.Fatal("remove the other duplicate")
	}
	if tr.Remove(1, 5) {
		t.Fatal("no more [1,5]")
	}
	if got := tr.Stab(5); len(got) != 1 || got[0].Value != 3 {
		t.Fatalf("remaining stab(5)=%v", got)
	}
}

// 与暴力法随机对拍。
func TestAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	type iv struct{ lo, hi int64 }
	tr := New[int]()
	var ref []iv
	for i := 0; i < 2000; i++ {
		lo := int64(rng.Intn(1000))
		hi := lo + int64(rng.Intn(50))
		tr.Insert(lo, hi, i)
		ref = append(ref, iv{lo, hi})
	}
	for q := 0; q < 500; q++ {
		lo := int64(rng.Intn(1000))
		hi := lo + int64(rng.Intn(50))
		got := len(tr.Overlap(lo, hi))
		want := 0
		for _, r := range ref {
			if r.lo <= hi && lo <= r.hi {
				want++
			}
		}
		if got != want {
			t.Fatalf("overlap(%d,%d)=%d want %d", lo, hi, got, want)
		}
		anyGot := tr.Any(lo, hi)
		if anyGot != (want > 0) {
			t.Fatalf("Any(%d,%d)=%v want %v", lo, hi, anyGot, want > 0)
		}
	}
}
