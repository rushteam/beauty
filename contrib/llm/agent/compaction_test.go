package agent_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
	"github.com/rushteam/beauty/contrib/llm/agent/compaction"
)

func bigStr(n int) string { return strings.Repeat("x", n) }

func TestToolResults_BelowThreshold(t *testing.T) {
	c := &compaction.ToolResults{MaxTokens: 100000}
	in := []llm.Message{
		{Role: llm.User, Content: "hi"},
		{Role: llm.Tool, ToolCallID: "1", Content: bigStr(1000)},
	}
	out, err := c.Compact(nil, in)
	if err != nil || len(out) != 2 || out[1].Content != in[1].Content {
		t.Fatalf("未超阈值不应改动: %+v err=%v", out, err)
	}
}

func TestToolResults_TruncatesOldToolResults(t *testing.T) {
	c := &compaction.ToolResults{MaxTokens: 100, KeepRecent: 1, ToolResultMaxRunes: 50}
	in := []llm.Message{
		{Role: llm.User, Content: "问题"},
		{Role: llm.Assistant, ToolCalls: []llm.ToolCall{{ID: "1", Name: "t"}}},
		{Role: llm.Tool, ToolCallID: "1", Content: bigStr(4000)},
		{Role: llm.Assistant, ToolCalls: []llm.ToolCall{{ID: "2", Name: "t"}}},
		{Role: llm.Tool, ToolCallID: "2", Content: bigStr(4000)},
	}
	orig := in[2].Content
	out, err := c.Compact(nil, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 5 {
		t.Fatalf("条数不应变: %d", len(out))
	}
	if len([]rune(out[2].Content)) >= 4000 || !strings.Contains(out[2].Content, "compacted") {
		t.Fatalf("旧 tool 结果应被截断并带标记: len=%d", len([]rune(out[2].Content)))
	}
	if out[4].Content != in[4].Content {
		t.Fatalf("最近 KeepRecent 条不应被截断")
	}
	if in[2].Content != orig {
		t.Fatal("Compact 不应改动入参底层")
	}
}

func TestToolResults_OnlyToolResults(t *testing.T) {
	c := &compaction.ToolResults{MaxTokens: 10, KeepRecent: 1, ToolResultMaxRunes: 10}
	in := []llm.Message{
		{Role: llm.User, Content: bigStr(4000)},
		{Role: llm.Assistant, Content: "ok"},
	}
	out, err := c.Compact(nil, in)
	if err != nil || out[0].Content != in[0].Content {
		t.Fatal("user 大消息不应被截断")
	}
}

func TestRunner_Compaction(t *testing.T) {
	tool := agent.Func("t", "", nil, func(_ context.Context, _ json.RawMessage) (string, error) {
		return bigStr(4000), nil
	})
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "t"}}},
		{ToolCalls: []llm.ToolCall{{ID: "c2", Name: "t"}}},
		{Content: "done"},
	}}
	r := &agent.Runner{
		Client: fc,
		Tools:  []agent.Tool{tool},
		Compaction: &compaction.ToolResults{
			MaxTokens: 100, KeepRecent: 1, ToolResultMaxRunes: 50,
		},
	}
	if _, err := agent.CollectOutcome(r.Run(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.User, Content: "go"}}})).Final(); err != nil {
		t.Fatalf("run: %v", err)
	}
	msgs := fc.lastReq.Messages
	if len(msgs) != 5 {
		t.Fatalf("第三轮消息数应为 5: %d", len(msgs))
	}
	if !strings.Contains(msgs[2].Content, "compacted") {
		t.Fatalf("旧的大 tool 结果应被压缩投影: len=%d", len(msgs[2].Content))
	}
	if len([]rune(msgs[4].Content)) < 4000 {
		t.Fatalf("最近的 tool 结果应完整保留: len=%d", len([]rune(msgs[4].Content)))
	}
}
