package actrie

import (
	"strings"
	"testing"
	"unicode"
)

func TestFindAllOverlap(t *testing.T) {
	m := New()
	m.AddAll("he", "she", "his", "hers")
	m.Build()
	got := m.FindAll("ushers")
	// "ushers" 命中 she(1-4), he(2-4), hers(2-6)
	want := map[string]bool{"she": true, "he": true, "hers": true}
	if len(got) != 3 {
		t.Fatalf("got %d matches: %+v", len(got), got)
	}
	for _, mt := range got {
		if !want[mt.Pattern] {
			t.Fatalf("unexpected match %q", mt.Pattern)
		}
		// 校验起止下标切出的子串等于模式串
		sub := string([]rune("ushers")[mt.Start:mt.End])
		if sub != mt.Pattern {
			t.Fatalf("offset mismatch: sub=%q pattern=%q", sub, mt.Pattern)
		}
	}
}

func TestContains(t *testing.T) {
	m := New()
	m.AddAll("apple", "banana")
	if m.Contains("i like grapes") {
		t.Fatal("should not contain")
	}
	if !m.Contains("i like banana split") {
		t.Fatal("should contain banana")
	}
}

func TestChineseSensitiveWords(t *testing.T) {
	m := New()
	m.AddAll("敏感词", "违禁", "测试")
	text := "这是一段包含敏感词和违禁内容的测试文本"
	if !m.Contains(text) {
		t.Fatal("should contain sensitive words")
	}
	got := m.FindAll(text)
	if len(got) != 3 {
		t.Fatalf("want 3 matches, got %d: %+v", len(got), got)
	}
	masked := m.Replace(text, '*')
	if strings.Contains(masked, "敏感词") || strings.Contains(masked, "违禁") || strings.Contains(masked, "测试") {
		t.Fatalf("masked text still contains sensitive words: %q", masked)
	}
	if want := "这是一段包含***和**内容的**文本"; masked != want {
		t.Fatalf("Replace=%q want %q", masked, want)
	}
}

func TestFindFirst(t *testing.T) {
	m := New()
	m.AddAll("cat", "dog")
	mt, ok := m.FindFirst("a dog and a cat")
	if !ok || mt.Pattern != "dog" {
		t.Fatalf("FindFirst=%+v,%v want dog", mt, ok)
	}
	if _, ok := m.FindFirst("nothing here"); ok {
		t.Fatal("should find nothing")
	}
}

func TestReplaceOverlap(t *testing.T) {
	m := New()
	m.AddAll("ab", "bc")
	// "abc" 中 ab(0-2)与 bc(1-3)重叠,应整体遮罩为 ***
	if got := m.Replace("abc", '*'); got != "***" {
		t.Fatalf("Replace(abc)=%q want ***", got)
	}
}

func TestAutoBuild(t *testing.T) {
	m := New()
	m.Add("x")
	// 未显式 Build,查询应自动 Build
	if !m.Contains("axb") {
		t.Fatal("auto build failed")
	}
	if m.Size() != 1 {
		t.Fatalf("Size=%d want 1", m.Size())
	}
}

func TestEmpty(t *testing.T) {
	m := New()
	m.Add("")
	m.Build()
	if m.Size() != 0 {
		t.Fatal("empty pattern should be ignored")
	}
	if m.Contains("anything") {
		t.Fatal("empty matcher should match nothing")
	}
	if got := m.Replace("anything", '*'); got != "anything" {
		t.Fatalf("Replace with no patterns changed text: %q", got)
	}
}

func TestCount(t *testing.T) {
	m := New()
	m.AddAll("ab", "bc")
	// "ababc": ab@0, ab@2, bc@3
	got := m.Count("ababc")
	if got["ab"] != 2 || got["bc"] != 1 {
		t.Fatalf("Count=%v", got)
	}
}

func TestReplaceFuncLevels(t *testing.T) {
	m := New()
	m.AddAll("敏感", "广告")
	level := map[string]int{"敏感": 3, "广告": 1}
	got := m.ReplaceFunc("含敏感内容和广告", func(mt Match) string {
		if level[mt.Pattern] >= 3 {
			return "***"
		}
		return "**"
	})
	if want := "含***内容和**"; got != want {
		t.Fatalf("ReplaceFunc=%q want %q", got, want)
	}
}

func TestNormalizerFoldCase(t *testing.T) {
	m := New(WithNormalizer(FoldCase()))
	m.Add("Hello")
	if !m.Contains("say HELLO now") {
		t.Fatal("case-insensitive match failed")
	}
	if !m.Contains("hello") {
		t.Fatal("lower match failed")
	}
}

func TestNormalizerStripInterspersed(t *testing.T) {
	// 剥离非字母,命中 "F*u*c*k" / "f u c k";位置须映射回原文
	norm := Chain(Keep(unicode.IsLetter), FoldCase())
	m := New(WithNormalizer(norm))
	m.Add("fuck")
	text := "say F*u*c*k please"
	if !m.Contains(text) {
		t.Fatal("should match interspersed")
	}
	mt, ok := m.FindFirst(text)
	if !ok {
		t.Fatal("FindFirst failed")
	}
	// 原文中 "F*u*c*k" 起于下标 4(s a y space F...),止于 'k' 后
	sub := string([]rune(text)[mt.Start:mt.End])
	if sub != "F*u*c*k" {
		t.Fatalf("matched span=%q want F*u*c*k", sub)
	}
	// Replace 应遮罩整个原文片段
	masked := m.Replace(text, '*')
	if masked != "say ******* please" {
		t.Fatalf("Replace=%q want %q", masked, "say ******* please")
	}
}

func TestNormalizerVisualMap(t *testing.T) {
	m := New(WithNormalizer(VisualMap()))
	m.Add("color")
	cases := []string{
		"color", // 原样
		"c0l0r", // 数字伪装 0→o
		"COLOR", // 大写
		"ＣＯＬＯＲ", // 全角
		"cоlоr", // 西里尔 о
	}
	for _, c := range cases {
		if !m.Contains(c) {
			t.Fatalf("VisualMap should match %q", c)
		}
	}
	if m.Contains("xyzzy") {
		t.Fatal("should not match unrelated text")
	}
}

func TestNormalizerChineseMasking(t *testing.T) {
	// 中文敏感词 + 折叠;夹杂符号绕过
	norm := Chain(Keep(func(r rune) bool {
		return unicode.Is(unicode.Han, r) || unicode.IsLetter(r) || unicode.IsDigit(r)
	}), FoldCase())
	m := New(WithNormalizer(norm))
	m.Add("敏感词")
	text := "这是敏-感-词测试"
	if !m.Contains(text) {
		t.Fatal("should match interspersed chinese")
	}
	masked := m.Replace(text, '*')
	if strings.Contains(masked, "敏") {
		t.Fatalf("masked leaks: %q", masked)
	}
}
