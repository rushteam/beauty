package compaction_test

import (
	"context"
	"strings"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent/compaction"
)

func bigStr(n int) string { return strings.Repeat("x", n) }

func TestSlidingWindow(t *testing.T) {
	w := &compaction.SlidingWindow{MaxMessages: 3}
	in := []llm.Message{
		{Role: llm.User, Content: "1"},
		{Role: llm.Assistant, Content: "2"},
		{Role: llm.User, Content: "3"},
		{Role: llm.Assistant, Content: "4"},
	}
	out, err := w.Compact(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 || out[0].Content != "2" || out[2].Content != "4" {
		t.Fatalf("unexpected window: %+v", out)
	}
}

func TestSlidingWindow_PreserveSystem(t *testing.T) {
	w := &compaction.SlidingWindow{MaxMessages: 2, PreserveSystem: true}
	in := []llm.Message{
		{Role: llm.System, Content: "sys"},
		{Role: llm.User, Content: "1"},
		{Role: llm.User, Content: "2"},
		{Role: llm.User, Content: "3"},
	}
	out, err := w.Compact(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 || out[0].Role != llm.System || out[2].Content != "3" {
		t.Fatalf("unexpected: %+v", out)
	}
}

func TestTruncation(t *testing.T) {
	tr := &compaction.Truncation{MaxTokens: 50, KeepRecent: 1, MaxRunes: 20}
	in := []llm.Message{
		{Role: llm.User, Content: bigStr(2000)},
		{Role: llm.Assistant, Content: "ok"},
	}
	out, err := tr.Compact(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out[0].Content, "compacted") {
		t.Fatalf("old user message should be truncated: %q", out[0].Content)
	}
	if out[1].Content != "ok" {
		t.Fatal("recent message should stay intact")
	}
}

func TestSummarization(t *testing.T) {
	sum := &compaction.Summarization{
		MaxMessages: 2,
		KeepRecent:  1,
		Summarize: func(_ context.Context, msgs []llm.Message) (string, error) {
			return "earlier talk about " + msgs[0].Content, nil
		},
	}
	in := []llm.Message{
		{Role: llm.User, Content: "weather"},
		{Role: llm.Assistant, Content: "sunny"},
		{Role: llm.User, Content: "thanks"},
	}
	out, err := sum.Compact(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].Role != llm.System || !strings.Contains(out[0].Content, "weather") {
		t.Fatalf("unexpected summary projection: %+v", out)
	}
	if out[1].Content != "thanks" {
		t.Fatal("recent message missing")
	}
}

func TestChain(t *testing.T) {
	chain := compaction.Chain(
		&compaction.SlidingWindow{MaxMessages: 2},
		&compaction.Truncation{MaxTokens: 10, KeepRecent: 1, MaxRunes: 5},
	)
	in := []llm.Message{
		{Role: llm.User, Content: bigStr(100)},
		{Role: llm.Assistant, Content: bigStr(100)},
		{Role: llm.User, Content: "hi"},
	}
	out, err := chain.Compact(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 after window, got %d", len(out))
	}
}

func TestBeforeModelHook(t *testing.T) {
	hook := compaction.BeforeModelHook(&compaction.SlidingWindow{MaxMessages: 1})
	req := llm.Request{Messages: []llm.Message{
		{Role: llm.User, Content: "a"},
		{Role: llm.User, Content: "b"},
	}}
	if err := hook(context.Background(), 1, &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 1 || req.Messages[0].Content != "b" {
		t.Fatalf("hook did not compact: %+v", req.Messages)
	}
}

func TestSnip_HeadTail(t *testing.T) {
	s := &compaction.Snip{MaxRunes: 20, PrefixRunes: 6, SuffixRunes: 4}
	in := []llm.Message{
		{Role: llm.User, Content: strings.Repeat("u", 100)},
		{Role: llm.Tool, ToolCallID: "t1", Content: "ABCDEFGHIJKLMNOPQRSTUVWXYZ"},
	}
	out, err := s.Compact(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Content != in[0].Content {
		t.Fatal("user message should not snip")
	}
	if !strings.Contains(out[1].Content, "snip") || !strings.HasPrefix(out[1].Content, "ABCDEF") {
		t.Fatalf("snip = %q", out[1].Content)
	}
	if !strings.Contains(out[1].Content, "WXYZ") {
		t.Fatalf("missing tail: %q", out[1].Content)
	}
}

func TestMicrocompact_KeepsRecent(t *testing.T) {
	m := &compaction.Microcompact{KeepRecent: 1}
	in := []llm.Message{
		{Role: llm.Tool, ToolCallID: "a", Content: "old"},
		{Role: llm.User, Content: "q"},
		{Role: llm.Tool, ToolCallID: "b", Content: "new"},
	}
	out, err := m.Compact(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Content == "old" {
		t.Fatal("old tool result should be dropped")
	}
	if out[2].Content != "new" {
		t.Fatalf("recent tool = %q", out[2].Content)
	}
}

func TestAutoLadder_Steps(t *testing.T) {
	var levels []compaction.CompactLevel
	a := compaction.AutoLadder(200, nil)
	a.Estimate = func(s string) int { return len(s) }
	a.Snip = &compaction.Snip{MaxRunes: 30, PrefixRunes: 10, SuffixRunes: 5}
	a.Micro = &compaction.Microcompact{KeepRecent: 1}
	a.OnState = func(st compaction.WindowState) { levels = append(levels, st.Level) }

	in := []llm.Message{
		{Role: llm.Tool, ToolCallID: "a", Content: strings.Repeat("a", 80)},
		{Role: llm.Tool, ToolCallID: "b", Content: strings.Repeat("b", 80)},
	}
	out, err := a.Compact(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(levels) == 0 || levels[0] == compaction.CompactOK {
		t.Fatalf("should trigger auto, levels=%v", levels)
	}
	if out[0].Content == in[0].Content && out[1].Content == in[1].Content {
		t.Fatal("expected snip or microcompact to change tool results")
	}
}
