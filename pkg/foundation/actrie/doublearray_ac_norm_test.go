package actrie

import (
	"testing"
	"unicode"
)

func TestDoubleArrayACFoldCase(t *testing.T) {
	ac := BuildDoubleArrayACWith([]string{"Bad"}, []Option{WithNormalizer(FoldCase())})
	text := "a bAd dog"
	ms := ac.FindAll(text)
	if len(ms) != 1 {
		t.Fatalf("期望 1 个命中, got %d", len(ms))
	}
	if ms[0].Pattern != "Bad" {
		t.Fatalf("应回报原文模式串 Bad, got %q", ms[0].Pattern)
	}
	if got := text[ms[0].Start:ms[0].End]; got != "bAd" {
		t.Fatalf("位置未映射回原文: text[%d:%d]=%q", ms[0].Start, ms[0].End, got)
	}
}

func TestDoubleArrayACStripInterference(t *testing.T) {
	norm := Chain(
		Keep(func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }),
		FoldCase(),
	)
	ac := BuildDoubleArrayACWith([]string{"fuck"}, []Option{WithNormalizer(norm)})
	text := "say f*u*c*k! now"
	ms := ac.FindAll(text)
	if len(ms) != 1 {
		t.Fatalf("期望命中插入干扰符的变体, got %d", len(ms))
	}
	if got := text[ms[0].Start:ms[0].End]; got != "f*u*c*k" {
		t.Fatalf("位置映射错误: %q", got)
	}
	if !ac.Contains(text) {
		t.Fatal("Contains 应为真")
	}
}

func TestDoubleArrayACVisualMap(t *testing.T) {
	ac := BuildDoubleArrayACWith([]string{"cat"}, []Option{WithNormalizer(VisualMap())})
	// с/а 为西里尔同形字(各 2 字节),t 为 ASCII。
	text := "a саt here"
	ms := ac.FindAll(text)
	if len(ms) != 1 {
		t.Fatalf("期望同形字变体命中, got %d", len(ms))
	}
	if got := text[ms[0].Start:ms[0].End]; got != "саt" {
		t.Fatalf("多字节位置映射错误: %q", got)
	}
}

func TestDoubleArrayACNormalizeMultibytePattern(t *testing.T) {
	// 中文 + 剥离空格:模式 "敏感" 命中 "敏  感"。
	norm := Keep(func(r rune) bool { return !unicode.IsSpace(r) })
	ac := BuildDoubleArrayACWith([]string{"敏感"}, []Option{WithNormalizer(norm)})
	text := "这是敏  感词"
	ms := ac.FindAll(text)
	if len(ms) != 1 {
		t.Fatalf("期望跨空格命中, got %d", len(ms))
	}
	if got := text[ms[0].Start:ms[0].End]; got != "敏  感" {
		t.Fatalf("中文位置映射错误: %q", got)
	}
}

func TestDoubleArrayACNoNormalizeStillWorks(t *testing.T) {
	// 不设归一化时走字节快路径,大小写敏感。
	ac := BuildDoubleArrayAC("Bad")
	if ac.Contains("bad") {
		t.Fatal("未归一化不应命中大小写不同的 bad")
	}
	if !ac.Contains("Bad") {
		t.Fatal("应命中 Bad")
	}
}
