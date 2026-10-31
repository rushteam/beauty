package roaring

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

func TestAddContainsRemove(t *testing.T) {
	b := New()
	if !b.Add(42) || b.Add(42) {
		t.Fatal("Add dedup")
	}
	if !b.Contains(42) || b.Contains(43) {
		t.Fatal("Contains")
	}
	if b.Len() != 1 {
		t.Fatalf("len=%d", b.Len())
	}
	if !b.Remove(42) || b.Remove(42) {
		t.Fatal("Remove")
	}
	if !b.IsEmpty() {
		t.Fatal("should be empty")
	}
}

func TestCrossContainersAndSortedRange(t *testing.T) {
	b := Of(1, 70000, 3, 140000, 2) // 跨 3 个高 16 位桶
	got := b.ToArray()
	want := []uint32{1, 2, 3, 70000, 140000}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ToArray=%v want %v", got, want)
	}
}

// 让一个桶升级为位图容器(>4096),再降级验证一致性。
func TestContainerUpgradeDowngrade(t *testing.T) {
	b := New()
	for i := uint32(0); i < 5000; i++ {
		b.Add(i)
	}
	if b.containers[0].isArray() {
		t.Fatal("expected bitmap container after 5000 adds")
	}
	if b.Len() != 5000 {
		t.Fatalf("len=%d", b.Len())
	}
	for i := uint32(0); i < 2000; i++ {
		b.Remove(i)
	}
	if !b.containers[0].isArray() {
		t.Fatal("expected downgrade to array after removals")
	}
	if b.Len() != 3000 || b.Contains(1999) || !b.Contains(2000) {
		t.Fatalf("post-downgrade state wrong: len=%d", b.Len())
	}
}

func TestSetOps(t *testing.T) {
	a := Of(1, 2, 3, 100000, 100001)
	b := Of(2, 3, 4, 100001, 200000)
	if got := Or(a, b).ToArray(); !reflect.DeepEqual(got, []uint32{1, 2, 3, 4, 100000, 100001, 200000}) {
		t.Fatalf("Or=%v", got)
	}
	if got := And(a, b).ToArray(); !reflect.DeepEqual(got, []uint32{2, 3, 100001}) {
		t.Fatalf("And=%v", got)
	}
	if got := AndNot(a, b).ToArray(); !reflect.DeepEqual(got, []uint32{1, 100000}) {
		t.Fatalf("AndNot=%v", got)
	}
	// 入参不被改动
	if a.Len() != 5 || b.Len() != 5 {
		t.Fatal("set ops mutated inputs")
	}
}

// 与 map[uint32]bool 随机对拍。
func TestAgainstMap(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	b := New()
	ref := map[uint32]bool{}
	for i := 0; i < 20000; i++ {
		v := rng.Uint32() % 300000
		switch rng.Intn(3) {
		case 0, 1:
			b.Add(v)
			ref[v] = true
		default:
			b.Remove(v)
			delete(ref, v)
		}
	}
	if int(b.Len()) != len(ref) {
		t.Fatalf("len mismatch: %d vs %d", b.Len(), len(ref))
	}
	want := make([]uint32, 0, len(ref))
	for v := range ref {
		if !b.Contains(v) {
			t.Fatalf("missing %d", v)
		}
		want = append(want, v)
	}
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	if got := b.ToArray(); !reflect.DeepEqual(got, want) {
		t.Fatal("ToArray mismatch with ref set")
	}
}
