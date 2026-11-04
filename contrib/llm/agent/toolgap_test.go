package agent_test

import (
	"context"
	"errors"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

func TestFillMissingToolResults(t *testing.T) {
	ctx := context.Background()
	msgs := []llm.Message{
		{Role: llm.User, Content: "hi"},
		{Role: llm.Assistant, ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: "echo"},
			{ID: "c2", Name: "other"},
		}},
		{Role: llm.Tool, ToolCallID: "c2", Content: "kept"},
		{Role: llm.User, Content: "next"},
	}
	got, err := agent.FillMissingToolResults(ctx, msgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("len = %d, want 5: %+v", len(got), got)
	}
	gap := got[2]
	if gap.Role != llm.Tool || gap.ToolCallID != "c1" || gap.Content == "" {
		t.Fatalf("gap = %+v", gap)
	}
	if got[3].ToolCallID != "c2" || got[3].Content != "kept" {
		t.Fatalf("existing result moved or rewritten: %+v", got[3])
	}

	again, err := agent.FillMissingToolResults(ctx, got, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != len(got) {
		t.Fatalf("second pass should be stable, len %d", len(again))
	}
}

func TestRunner_FillsMissingToolResultBeforeModel(t *testing.T) {
	ctx := context.Background()
	fc := &fakeClient{steps: []*llm.Response{{Content: "done"}}}
	r := &agent.Runner{
		Client: fc,
		MissingToolResult: func(_ context.Context, call llm.ToolCall) (string, error) {
			return "gap:" + call.Name, nil
		},
	}
	out := agent.CollectOutcome(r.Run(ctx, llm.Request{Messages: []llm.Message{
		{Role: llm.User, Content: "hi"},
		{Role: llm.Assistant, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo"}}},
	}}))
	if _, err := out.Final(); err != nil {
		t.Fatal(err)
	}
	msgs := fc.lastReq.Messages
	if len(msgs) != 3 || msgs[2].Role != llm.Tool || msgs[2].Content != "gap:echo" || msgs[2].ToolCallID != "c1" {
		t.Fatalf("model messages = %+v", msgs)
	}
}

func TestRunner_MissingToolResultError(t *testing.T) {
	ctx := context.Background()
	fc := &fakeClient{steps: []*llm.Response{{Content: "done"}}}
	want := errors.New("nope")
	r := &agent.Runner{
		Client: fc,
		MissingToolResult: func(context.Context, llm.ToolCall) (string, error) {
			return "", want
		},
	}
	out := agent.CollectOutcome(r.Run(ctx, llm.Request{Messages: []llm.Message{
		{Role: llm.Assistant, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo"}}},
	}}))
	if _, err := out.Final(); !errors.Is(err, want) {
		t.Fatalf("err = %v", err)
	}
	if fc.genCalls != 0 {
		t.Fatal("补结果失败时不应调用模型")
	}
}
