package skiplist

import (
	"cmp"
	"math/rand"
	"sort"
	"testing"
)

func newIntList() *SkipList[int, string] {
	return New[int, string](cmp.Compare[int])
}

func TestSetGetDelete(t *testing.T) {
	s := newIntList()
	if existed := s.Set(1, "a"); existed {
		t.Fatal("first set should not exist")
	}
	if existed := s.Set(1, "b"); !existed {
		t.Fatal("second set same key should report existed")
	}
	if v, ok := s.Get(1); !ok || v != "b" {
		t.Fatalf("Get(1)=%q,%v want b,true", v, ok)
	}
	if _, ok := s.Get(99); ok {
		t.Fatal("Get(99) should be false")
	}
	if s.Len() != 1 {
		t.Fatalf("Len=%d want 1", s.Len())
	}
	if !s.Delete(1) {
		t.Fatal("Delete(1) should succeed")
	}
	if s.Delete(1) {
		t.Fatal("Delete(1) again should fail")
	}
	if s.Len() != 0 {
		t.Fatalf("Len=%d want 0", s.Len())
	}
}

func TestOrderedAndRank(t *testing.T) {
	s := newIntList()
	nums := []int{5, 3, 9, 1, 7, 2, 8, 4, 6, 0}
	for _, n := range nums {
		s.Set(n, "")
	}
	// 升序遍历
	var got []int
	s.Range(func(k int, _ string) bool { got = append(got, k); return true })
	want := append([]int(nil), nums...)
	sort.Ints(want)
	if len(got) != len(want) {
		t.Fatalf("range len=%d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("range[%d]=%d want %d", i, got[i], want[i])
		}
	}
	// Rank:0 是第 1 名,9 是第 10 名
	if r := s.Rank(0); r != 1 {
		t.Fatalf("Rank(0)=%d want 1", r)
	}
	if r := s.Rank(9); r != 10 {
		t.Fatalf("Rank(9)=%d want 10", r)
	}
	if r := s.Rank(100); r != 0 {
		t.Fatalf("Rank(missing)=%d want 0", r)
	}
	// ByRank 与 Rank 互逆
	for i := 1; i <= 10; i++ {
		k, _, ok := s.ByRank(i)
		if !ok {
			t.Fatalf("ByRank(%d) not ok", i)
		}
		if s.Rank(k) != i {
			t.Fatalf("Rank(ByRank(%d))=%d want %d", i, s.Rank(k), i)
		}
	}
}

func TestMinMaxRangeFrom(t *testing.T) {
	s := newIntList()
	for _, n := range []int{10, 20, 30, 40, 50} {
		s.Set(n, "")
	}
	if k, _, ok := s.Min(); !ok || k != 10 {
		t.Fatalf("Min=%d want 10", k)
	}
	if k, _, ok := s.Max(); !ok || k != 50 {
		t.Fatalf("Max=%d want 50", k)
	}
	var got []int
	s.RangeFrom(25, func(k int, _ string) bool { got = append(got, k); return true })
	want := []int{30, 40, 50}
	if len(got) != 3 || got[0] != want[0] || got[2] != want[2] {
		t.Fatalf("RangeFrom(25)=%v want %v", got, want)
	}
}

func TestEmptyList(t *testing.T) {
	s := newIntList()
	if _, _, ok := s.Min(); ok {
		t.Fatal("empty Min should be false")
	}
	if _, _, ok := s.Max(); ok {
		t.Fatal("empty Max should be false")
	}
	if _, _, ok := s.ByRank(1); ok {
		t.Fatal("empty ByRank should be false")
	}
}

// 与标准 map + sort 对拍,验证插入/删除后有序性与规模一致。
func TestFuzzAgainstMap(t *testing.T) {
	s := newIntList()
	ref := map[int]int{}
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 5000; i++ {
		k := rng.Intn(500)
		switch rng.Intn(3) {
		case 0, 1:
			v := rng.Int()
			s.Set(k, itoa(v))
			ref[k] = v
		case 2:
			delete(ref, k)
			s.Delete(k)
		}
	}
	if s.Len() != len(ref) {
		t.Fatalf("Len=%d want %d", s.Len(), len(ref))
	}
	keys := make([]int, 0, len(ref))
	for k := range ref {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	var got []int
	s.Range(func(k int, v string) bool {
		got = append(got, k)
		if v != itoa(ref[k]) {
			t.Fatalf("value mismatch at %d", k)
		}
		return true
	})
	for i := range keys {
		if got[i] != keys[i] {
			t.Fatalf("order mismatch at %d: %d vs %d", i, got[i], keys[i])
		}
	}
	// 名次连续
	for i, k := range keys {
		if s.Rank(k) != i+1 {
			t.Fatalf("Rank(%d)=%d want %d", k, s.Rank(k), i+1)
		}
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
