package agent_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
	"github.com/rushteam/beauty/contrib/llm/agent/checkpoint"
)

func TestChainCheckpointEvents(t *testing.T) {
	store := agent.NewMemoryCheckpointStore()
	step1 := &agent.Runner{
		Name:   "step1",
		Client: &fakeClient{steps: []*llm.Response{{Content: "a"}}},
	}
	step2 := &agent.Runner{
		Name:   "step2",
		Client: &fakeClient{steps: []*llm.Response{{Content: "b"}}},
		Store:  agent.NewMemoryCheckpointStore(),
	}
	chain := &agent.Chain{
		Name:  "pipe",
		Store: store,
		Steps: []agent.ChainStep{
			{Name: "step1", Runner: step1},
			{Name: "step2", Runner: step2},
		},
	}

	out := agent.CollectOutcome(chain.Run(context.Background(), llm.Request{
		Model:    "m",
		Messages: []llm.Message{{Role: llm.User, Content: "go"}},
	}))
	if !out.IsDone() {
		t.Fatalf("expected done, got %s", out.Status)
	}

	events, err := chain.LoadUIEvents(context.Background(), out.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("expected chain checkpoint events")
	}
	hasStarted, hasCompleted := false, false
	for _, ev := range events {
		switch ev.Type {
		case checkpoint.TypeRunStarted:
			hasStarted = true
		case checkpoint.TypeRunCompleted:
			hasCompleted = true
		case checkpoint.TypeAgentSpawned:
			if ev.ChildRunID == "" {
				t.Error("spawned missing child_run_id")
			}
		}
	}
	if !hasStarted || !hasCompleted {
		t.Fatalf("started=%v completed=%v", hasStarted, hasCompleted)
	}
}

func TestChainCheckpointPause(t *testing.T) {
	store := agent.NewMemoryCheckpointStore()
	askTool := echoTool()
	askTool.Permission = agent.PermitAsk
	step := &agent.Runner{
		Name: "ask",
		Client: &fakeClient{steps: []*llm.Response{
			{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo", Arguments: json.RawMessage(`{"x":1}`)}}},
			{Content: "ok"},
		}},
		Store: agent.NewMemoryCheckpointStore(),
		Tools: []agent.Tool{askTool},
	}
	chain := &agent.Chain{
		Name:  "pipe",
		Store: store,
		Steps: []agent.ChainStep{{Name: "ask", Runner: step}},
	}

	out := agent.CollectOutcome(chain.Run(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.User, Content: "hi"}}}))
	if !out.IsPaused() {
		t.Fatalf("expected paused, got %s", out.Status)
	}
	snap, _ := store.Load(context.Background(), out.RunID)
	if snap == nil || snap.EventCount <= 0 {
		t.Fatal("expected checkpoint snap with EventCount")
	}
	cont := chain.Continue(context.Background(), out.RunID, []agent.Resolution{{ID: "c1", Approved: true}})
	if !cont.IsDone() {
		t.Fatalf("expected done after continue, got %s", cont.Status)
	}
}
