package compaction_test

import (
	"context"
	"strings"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent/compaction"
)

// 固定估算: 每字符 1 token,便于测试阈值。
func charTokens(s string) int { return len(s) }

func TestNewMessageIndex_Grouping(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.System, Content: "sys"},
		{Role: llm.User, Content: "hello"},
		{Role: llm.Assistant, Content: "hi"},
		{
			Role: llm.Assistant,
			ToolCalls: []llm.ToolCall{
				{ID: "call_1", Name: "search"},
				{ID: "call_2", Name: "fetch"},
			},
		},
		{Role: llm.Tool, ToolCallID: "call_1", Content: "result-1"},
		{Role: llm.Tool, ToolCallID: "call_2", Content: "result-2"},
		{Role: llm.User, Content: "thanks"},
	}

	idx := compaction.NewMessageIndex(msgs, charTokens)
	if len(idx.Groups) != 5 {
		t.Fatalf("expected 5 groups, got %d", len(idx.Groups))
	}

	kinds := []compaction.GroupKind{
		compaction.GroupSystem,
		compaction.GroupUserTurn,
		compaction.GroupAssistant,
		compaction.GroupToolChain,
		compaction.GroupUserTurn,
	}
	for i, want := range kinds {
		if idx.Groups[i].Kind != want {
			t.Fatalf("group[%d]: want kind %d, got %d", i, want, idx.Groups[i].Kind)
		}
	}

	chain := idx.Groups[3]
	if len(chain.Messages) != 3 {
		t.Fatalf("tool chain should have 3 messages (assistant + 2 tools), got %d", len(chain.Messages))
	}
	if chain.Messages[0].Role != llm.Assistant || chain.Messages[1].Role != llm.Tool || chain.Messages[2].Role != llm.Tool {
		t.Fatalf("unexpected tool chain roles: %+v", chain.Messages)
	}
}

func TestNewMessageIndex_ToolChainAtomic(t *testing.T) {
	msgs := []llm.Message{
		{
			Role:      llm.Assistant,
			ToolCalls: []llm.ToolCall{{ID: "c1", Name: "run"}},
		},
		{Role: llm.Tool, ToolCallID: "c1", Content: strings.Repeat("x", 100)},
		{Role: llm.Assistant, Content: "done"},
	}

	idx := compaction.NewMessageIndex(msgs, charTokens)
	if len(idx.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(idx.Groups))
	}
	if idx.Groups[0].Kind != compaction.GroupToolChain || len(idx.Groups[0].Messages) != 2 {
		t.Fatal("assistant+tool must be one atomic GroupToolChain")
	}
	if idx.Groups[1].Kind != compaction.GroupAssistant {
		t.Fatal("text assistant should be GroupAssistant")
	}
}

func TestMessageIndex_ExcludeOldestSkipsSystem(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.System, Content: "sys"},
		{Role: llm.User, Content: "u1"},
		{Role: llm.Assistant, Content: "a1"},
		{Role: llm.User, Content: "u2"},
	}

	idx := compaction.NewMessageIndex(msgs, charTokens)
	idx.ExcludeOldest(2)

	if idx.Groups[0].Excluded {
		t.Fatal("system group must never be excluded")
	}
	if !idx.Groups[1].Excluded || !idx.Groups[2].Excluded {
		t.Fatal("oldest non-system groups should be excluded")
	}
	if idx.Groups[3].Excluded {
		t.Fatal("newest user group should remain")
	}
}

func TestMessageIndex_Flatten(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.System, Content: "sys"},
		{Role: llm.User, Content: "drop-me"},
		{Role: llm.Assistant, Content: "keep-me"},
	}

	idx := compaction.NewMessageIndex(msgs, charTokens)
	idx.ExcludeOldest(1)

	out := idx.Flatten()
	if len(out) != 2 {
		t.Fatalf("expected 2 messages after flatten, got %d", len(out))
	}
	if out[0].Content != "sys" || out[1].Content != "keep-me" {
		t.Fatalf("unexpected flatten: %+v", out)
	}
}

func TestMessageIndex_TotalTokens(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.User, Content: "abc"},
		{Role: llm.Assistant, Content: "de"},
	}
	idx := compaction.NewMessageIndex(msgs, charTokens)
	if got := idx.TotalTokens(); got != 5 {
		t.Fatalf("expected 5 tokens, got %d", got)
	}
	idx.ExcludeOldest(1)
	if got := idx.TotalTokens(); got != 2 {
		t.Fatalf("after exclude oldest, expected 2 tokens, got %d", got)
	}
}

func TestPipeline_GroupStrategies(t *testing.T) {
	p := &compaction.Pipeline{
		Estimate: charTokens,
		Strategies: []compaction.GroupStrategy{
			compaction.GroupStrategyFunc(func(_ context.Context, idx *compaction.MessageIndex) error {
				idx.ExcludeOldest(1)
				return nil
			}),
		},
	}
	in := []llm.Message{
		{Role: llm.System, Content: "s"},
		{Role: llm.User, Content: "old"},
		{Role: llm.User, Content: "new"},
	}
	out, err := p.Compact(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].Content != "s" || out[1].Content != "new" {
		t.Fatalf("unexpected pipeline output: %+v", out)
	}
}
