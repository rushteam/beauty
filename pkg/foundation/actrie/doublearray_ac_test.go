package actrie

import (
	"sort"
	"strings"
	"testing"
)

func TestDoubleArrayACBasic(t *testing.T) {
	ac := BuildDoubleArrayAC("he", "she", "his", "hers")
	if ac.Size() != 4 {
		t.Fatalf("size=%d", ac.Size())
	}
	// 经典 AC 测试串 "ushers":she(1-4)、he(2-4)、hers(2-6)。
	matches := ac.FindAll("ushers")
	got := map[string]bool{}
	for _, m := range matches {
		got[m.Pattern] = true
		// 校验位置切片与模式一致。
		if "ushers"[m.Start:m.End] != m.Pattern {
			t.Fatalf("slice mismatch: %q vs %q", "ushers"[m.Start:m.End], m.Pattern)
		}
	}
	for _, want := range []string{"she", "he", "hers"} {
		if !got[want] {
			t.Fatalf("missing %q in %v", want, got)
		}
	}
	if got["his"] {
		t.Fatal("his should not match")
	}
}

func TestDoubleArrayACContains(t *testing.T) {
	ac := BuildDoubleArrayAC("敏感词", "违禁", "测试")
	if !ac.Contains("这是一段包含违禁内容的文本") {
		t.Fatal("should contain 违禁")
	}
	if ac.Contains("干净的文本") {
		t.Fatal("should not contain anything")
	}
}

// 与基于 map 的 Matcher 随机对拍:两者命中集合应一致(按 Pattern+起止)。
func TestDoubleArrayACAgainstMatcher(t *testing.T) {
	patterns := []string{"a", "ab", "abc", "bc", "c", "cat", "dog", "do", "god", "国", "中国", "国人"}
	ac := BuildDoubleArrayAC(patterns...)
	ref := New()
	ref.AddAll(patterns...)
	ref.Build()

	texts := []string{"abcabc", "catdog", "中国人民", "abcdefg", "godogcat", ""}
	norm := func(ms []Match, text string) []string {
		out := make([]string, 0, len(ms))
		for _, m := range ms {
			out = append(out, m.Pattern)
		}
		sort.Strings(out)
		return out
	}
	for _, txt := range texts {
		da := norm(ac.FindAll(txt), txt)
		// Matcher 用 rune 位置,这里只比较命中的「模式集合(含重复次数)」。
		refMs := ref.FindAll(txt)
		rm := make([]string, 0, len(refMs))
		for _, m := range refMs {
			rm = append(rm, m.Pattern)
		}
		sort.Strings(rm)
		if strings.Join(da, ",") != strings.Join(rm, ",") {
			t.Fatalf("text %q: DA=%v Matcher=%v", txt, da, rm)
		}
	}
}
