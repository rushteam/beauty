package checkpoint_test

import (
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent/checkpoint"
)

func TestReplayMessages(t *testing.T) {
	events := []checkpoint.Event{
		{Type: checkpoint.TypeUserMessage, Message: &llm.Message{Role: llm.User, Content: "hi"}},
		{Type: checkpoint.TypeModelResponse, Response: &llm.Response{
			ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo"}},
		}},
		{Type: checkpoint.TypeToolResult, ToolCall: &llm.ToolCall{ID: "c1"}, Result: "ok"},
	}
	msgs := checkpoint.ReplayMessages(events)
	if len(msgs) != 3 {
		t.Fatalf("got %d messages", len(msgs))
	}
}

func TestBuildRunTree(t *testing.T) {
	events := []checkpoint.Event{
		{Type: checkpoint.TypeRunStarted, RunID: "parent", AgentName: "parent", Depth: 0},
		{Type: checkpoint.TypeAgentSpawned, RunID: "parent", ChildRunID: "child", Source: "tool:researcher", Step: 1},
		{Type: checkpoint.TypeRunPaused, RunID: "parent", Step: 1},
	}
	root := checkpoint.BuildRunTree(events)
	if root == nil || root.RunID != "parent" {
		t.Fatalf("root = %+v", root)
	}
	if len(root.Children) != 1 || root.Children[0].RunID != "child" {
		t.Fatalf("children = %+v", root.Children)
	}
}
