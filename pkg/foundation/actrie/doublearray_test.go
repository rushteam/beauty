package actrie

import (
	"reflect"
	"sort"
	"testing"
)

func TestDATExactMatch(t *testing.T) {
	dat := BuildDoubleArray(map[string]int{
		"北京": 1, "北京大学": 2, "大学": 3, "清华": 4, "a": 5, "ab": 6, "abc": 7,
	})
	if dat.Size() != 7 {
		t.Fatalf("size=%d", dat.Size())
	}
	for k, want := range map[string]int{"北京": 1, "北京大学": 2, "大学": 3, "清华": 4, "a": 5, "ab": 6, "abc": 7} {
		v, ok := dat.Get(k)
		if !ok || v != want {
			t.Fatalf("Get(%q)=%d,%v want %d", k, v, ok, want)
		}
	}
	for _, k := range []string{"北", "北京大", "大", "清", "abcd", "", "x"} {
		if _, ok := dat.Get(k); ok {
			t.Fatalf("Get(%q) should miss", k)
		}
	}
}

func TestDATCommonPrefixSearch(t *testing.T) {
	dat := BuildDoubleArray(map[string]string{
		"北京":   "city",
		"北京大学": "university",
		"大学":   "edu",
	})
	hits := dat.CommonPrefixSearch("北京大学城")
	var got []int
	for _, h := range hits {
		got = append(got, h.ByteLen)
	}
	// "北京"(6 字节) 与 "北京大学"(12 字节) 都是前缀。
	want := []int{6, 12}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("prefix lens=%v want %v", got, want)
	}
	if hits[0].Value != "city" || hits[1].Value != "university" {
		t.Fatalf("values=%v", hits)
	}
}

func TestDATLongestPrefix(t *testing.T) {
	dat := BuildDoubleArraySet([]string{"a", "ab", "abc", "abxyz"})
	p, _, ok := dat.LongestPrefix("abcdefg")
	if !ok || p != "abc" {
		t.Fatalf("longest=%q ok=%v", p, ok)
	}
	if _, _, ok := dat.LongestPrefix("xyz"); ok {
		t.Fatal("xyz should have no prefix")
	}
}

func TestDATEmptyKey(t *testing.T) {
	dat := BuildDoubleArray(map[string]int{"": 9, "x": 1})
	if v, ok := dat.Get(""); !ok || v != 9 {
		t.Fatalf("empty key: %d %v", v, ok)
	}
}

// 随机对拍:DAT 的精确匹配必须与 map 一致。
func TestDATAgainstMap(t *testing.T) {
	keys := []string{
		"go", "golang", "gopher", "good", "goose", "google", "go-lang",
		"状态机", "状态", "状元", "一致性哈希", "一致性", "一", "", "zzz",
	}
	ref := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		ref[k] = struct{}{}
	}
	dat := BuildDoubleArraySet(keys)

	probes := append([]string{}, keys...)
	probes = append(probes, "g", "goo", "gol", "状", "一致", "一致性哈", "zz", "zzzz")
	sort.Strings(probes)
	for _, p := range probes {
		_, want := ref[p]
		if got := dat.Contains(p); got != want {
			t.Fatalf("Contains(%q)=%v want %v", p, got, want)
		}
	}
}
