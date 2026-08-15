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
