package agent_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
	"github.com/rushteam/beauty/contrib/llm/agent/checkpoint"
)

func TestCheckpointPauseResumeReplay(t *testing.T) {
	store := agent.NewMemoryCheckpointStore()
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo", Arguments: json.RawMessage(`{"x":1}`)}}},
		{Content: "ok"},
	}}
	tool := echoTool()
	tool.Permission = agent.PermitAsk
	r := &agent.Runner{
		Client: fc,
		Store:  store,
		Tools:  []agent.Tool{tool},
	}

	ctx := context.Background()
	out := agent.CollectOutcome(r.Run(ctx, llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.User, Content: "hi"}}}))
	if !out.IsPaused() {
		t.Fatalf("expected paused, got %s", out.Status)
	}
	if fc.genCalls != 1 {
		t.Fatalf("pause 前不应继续 Generate, genCalls=%d", fc.genCalls)
	}

	snap, err := store.Load(ctx, out.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if snap.EventCount <= 0 {
		t.Fatal("expected EventCount > 0")
	}
	if len(snap.Messages) != 0 {
		t.Fatal("checkpoint snapshot should not store Messages inline")
	}

	events, _ := store.LoadEvents(ctx, out.RunID)
	if len(events) == 0 {
		t.Fatal("expected checkpoint events")
	}
	if events[0].Schema != checkpoint.SchemaVersion {
		t.Errorf("schema = %q", events[0].Schema)
	}

	loaded, err := store.Load(ctx, out.RunID)
	if err != nil {
		t.Fatal(err)
	}
	msgs := checkpoint.ReplayMessages(events[:loaded.EventCount])
	if len(msgs) < 2 {
		t.Fatalf("replayed messages = %d, want >=2", len(msgs))
	}

	cont := agent.CollectOutcome(r.Continue(ctx, out.RunID, []agent.Resolution{{ID: "c1", Approved: true}}))
	if !cont.IsDone() {
		t.Fatalf("expected done after continue, got %s err=%v", cont.Status, cont.Err)
	}

	events, _ = store.LoadEvents(ctx, out.RunID)
	hasResumed := false
	for _, ev := range events {
		if ev.Type == checkpoint.TypeRunResumed {
			hasResumed = true
		}
	}
	if !hasResumed {
		t.Error("missing run.resumed event")
	}
}

func TestCheckpointRunTree(t *testing.T) {
	store := agent.NewMemoryCheckpointStore()
	parent := &agent.Runner{
		Name:   "parent",
		Client: &fakeClient{steps: []*llm.Response{{Content: "parent done"}}},
		Store:  store,
	}
	child := &agent.Runner{
		Name:   "researcher",
		Client: &fakeClient{steps: []*llm.Response{{Content: "child done"}}},
		Store:  agent.NewMemoryCheckpointStore(),
	}
	parent.Tools = []agent.Tool{agent.AgentAsTool("researcher", "子 agent", child)}

	// 父 agent 直接完成(子 agent 同步完成),检查 run.started 事件。
	out := agent.CollectOutcome(parent.Run(context.Background(), llm.Request{
		Model:    "m",
		Messages: []llm.Message{{Role: llm.User, Content: "research X"}},
	}))
	if !out.IsDone() {
		t.Fatalf("expected done, got %s", out.Status)
	}

	events, _ := store.LoadEvents(context.Background(), out.RunID)
	if len(events) == 0 {
		t.Fatal("expected events on parent run")
	}
	if events[0].Type != checkpoint.TypeRunStarted {
		t.Errorf("first event = %s", events[0].Type)
	}
}

func TestCheckpointUIEventSchema(t *testing.T) {
	ev := checkpoint.NewEvent(checkpoint.TypeRunPaused, "run-1").WithStep(2)
	ev.Requirements = []checkpoint.Requirement{{ID: "c1", ToolCall: llm.ToolCall{ID: "c1", Name: "delete"}}}
	b, err := checkpoint.MarshalJSON(ev)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		t.Fatal("empty json")
	}
	if string(b) == "" {
		t.Fatal("expected json payload")
	}
}
