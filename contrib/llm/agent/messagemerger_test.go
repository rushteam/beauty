package agent_test

import (
	"context"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

// 相邻同角色合并,不同角色保留,Tool 消息永不合并。
func TestMergeConsecutive(t *testing.T) {
	in := []llm.Message{
		{Role: llm.User, Content: "a"},
		{Role: llm.User, Content: "b"},
		{Role: llm.Assistant, Content: "c"},
		{Role: llm.Tool, ToolCallID: "1", Content: "r1"},
		{Role: llm.Tool, ToolCallID: "2", Content: "r2"},
		{Role: llm.User, Content: "d"},
		{Role: llm.User, Content: "e"},
	}
	out := agent.MergeConsecutive(in, "\n")
	if len(out) != 5 {
		t.Fatalf("合并后应为 5 条, got %d: %+v", len(out), out)
	}
	if out[0].Role != llm.User || out[0].Content != "a\nb" {
		t.Fatalf("首条应合并为 user a\\nb: %+v", out[0])
	}
	if out[1].Role != llm.Assistant || out[1].Content != "c" {
		t.Fatalf("assistant 应保留: %+v", out[1])
	}
	if out[2].Role != llm.Tool || out[3].Role != llm.Tool {
		t.Fatalf("两条 Tool 不应合并: %+v", out)
	}
	if out[4].Content != "d\ne" {
		t.Fatalf("末尾两条 user 应合并: %+v", out[4])
	}
	// 不改动入参。
	if len(in) != 7 {
		t.Fatalf("入参被改动: %+v", in)
	}
}

// 合并时拼接 ToolCalls(assistant 连发)。
func TestMergeConsecutive_ToolCalls(t *testing.T) {
	in := []llm.Message{
		{Role: llm.Assistant, ToolCalls: []llm.ToolCall{{ID: "1", Name: "x"}}},
		{Role: llm.Assistant, ToolCalls: []llm.ToolCall{{ID: "2", Name: "y"}}},
	}
	out := agent.MergeConsecutive(in, "")
	if len(out) != 1 || len(out[0].ToolCalls) != 2 {
		t.Fatalf("assistant tool_calls 应拼接: %+v", out)
	}
	// 原切片的 ToolCalls 不被追加污染。
	if len(in[0].ToolCalls) != 1 {
		t.Fatalf("入参 ToolCalls 被改动: %+v", in[0])
	}
}

// 空文本一侧不产生多余分隔符。
func TestMergeConsecutive_EmptyContent(t *testing.T) {
	in := []llm.Message{
		{Role: llm.Assistant, Content: ""},
		{Role: llm.Assistant, Content: "hi"},
	}
	out := agent.MergeConsecutive(in, "\n\n")
	if len(out) != 1 || out[0].Content != "hi" {
		t.Fatalf("空文本不应产生前导分隔符: %+v", out)
	}
}

// Hook:在每轮请求发出前规整消息(与 Runner 集成)。
func TestMergeMessagesHook(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{{Content: "done"}}}
	r := &agent.Runner{Client: fc, Hooks: agent.Hooks{BeforeModel: agent.MergeMessagesHook("\n\n")}}
	_, err := r.Run(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{
		{Role: llm.User, Content: "一"},
		{Role: llm.User, Content: "二"},
	}}).Final()
	if err != nil {
		t.Fatal(err)
	}
	if got := fc.lastReq.Messages; len(got) != 1 || got[0].Content != "一\n\n二" {
		t.Fatalf("Hook 应在请求前合并连续 user: %+v", got)
	}
}
