package simhash

import (
	"math"
	"strings"
	"testing"
)

func tokens(s string) []string { return strings.Fields(s) }

func TestSimHashNearDuplicate(t *testing.T) {
	a := Hash(tokens("the quick brown fox jumps over the lazy dog")...)
	b := Hash(tokens("the quick brown fox jumps over the lazy cat")...) // 改一个词
	c := Hash(tokens("completely different unrelated sentence here now")...)

	if d := Distance(a, b); d > 8 {
		t.Fatalf("near-dup distance too large: %d", d)
	}
	if Distance(a, c) <= Distance(a, b) {
		t.Fatalf("unrelated should be farther: ac=%d ab=%d", Distance(a, c), Distance(a, b))
	}
	if Distance(a, a) != 0 || Similarity(a, a) != 1 {
		t.Fatal("self similarity")
	}
	if !Similar(a, a, 3) {
		t.Fatal("identical must be similar")
	}
}

func TestSimHashWeighted(t *testing.T) {
	// 带权与等价展开应一致。
	w := HashWeighted(map[string]int{"a": 2, "b": 1})
	expand := Hash("a", "a", "b")
	if w != expand {
		t.Fatalf("weighted %x != expanded %x", w, expand)
	}
}

func TestMinHashJaccard(t *testing.T) {
	m := NewMinHash(256)
	set := func(xs ...string) []string { return xs }
	a := set("a", "b", "c", "d", "e", "f", "g", "h")
	b := set("a", "b", "c", "d", "e", "f", "g", "z") // 7/9 交集 → J≈0.778
	sa, sb := m.Signature(a), m.Signature(b)

	est := Jaccard(sa, sb)
	// 真值:|A∩B|=7,|A∪B|=9 → 0.7778
	if math.Abs(est-0.7778) > 0.08 {
		t.Fatalf("jaccard est=%.3f want≈0.778", est)
	}
	if Jaccard(m.Signature(a), m.Signature(a)) != 1 {
		t.Fatal("identical sets → 1")
	}
	if j := Jaccard(m.Signature(set("x", "y")), m.Signature(set("p", "q"))); j > 0.1 {
		t.Fatalf("disjoint sets jaccard=%.3f", j)
	}
}

func TestMinHashDeterministic(t *testing.T) {
	// 不同实例、相同 k,签名必须可比(确定性参数)。
	m1, m2 := NewMinHash(64), NewMinHash(64)
	s1 := m1.Signature([]string{"foo", "bar", "baz"})
	s2 := m2.Signature([]string{"foo", "bar", "baz"})
	if Jaccard(s1, s2) != 1 {
		t.Fatal("same k across instances must produce identical signatures")
	}
}
