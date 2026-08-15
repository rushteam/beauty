package agent_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

func TestMemoryContinuationStore_CRUD(t *testing.T) {
	store := agent.NewMemoryContinuationStore()
	ctx := context.Background()

	token, err := store.Start(ctx)
	if err != nil || token == "" {
		t.Fatalf("Start: token=%q err=%v", token, err)
	}

	got, err := store.Poll(ctx, token)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if got.State != agent.ContinuationRunning {
		t.Fatalf("initial state = %d, want Running", got.State)
	}

	result := agent.ContinuationResult{
		State: agent.ContinuationCompleted,
		Outcome: &agent.RunOutcome{
			Status:   agent.StatusDone,
			Response: &llm.Response{Content: "ok"},
		},
	}
	if err := store.Update(ctx, token, result); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err = store.Poll(ctx, token)
	if err != nil {
		t.Fatalf("Poll after update: %v", err)
	}
	if got.State != agent.ContinuationCompleted {
		t.Fatalf("state = %d, want Completed", got.State)
	}
	if got.Outcome == nil || got.Outcome.Response.Content != "ok" {
		t.Fatalf("outcome = %+v", got.Outcome)
	}

	if err := store.Delete(ctx, token); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Poll(ctx, token); err == nil {
		t.Fatal("Poll after Delete should fail")
	}
}

func waitContinuation(ctx context.Context, store *agent.MemoryContinuationStore, token string, want agent.ContinuationState) *agent.ContinuationResult {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		res, err := store.Poll(ctx, token)
		if err != nil {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if res.State == want {
			return res
		}
		if res.State == agent.ContinuationFailed || res.State == agent.ContinuationCompleted || res.State == agent.ContinuationPaused {
			if res.State != want {
				panic("unexpected terminal state")
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

func TestRunAsync_Completes(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo", Arguments: json.RawMessage(`{"x":1}`)}}},
		{Content: "done"},
	}}
	r := &agent.Runner{Client: fc, Tools: []agent.Tool{echoTool()}}
	store := agent.NewMemoryContinuationStore()
	ctx := context.Background()

	token, err := agent.RunAsync(ctx, store, r, llm.Request{Model: "m"})
	if err != nil {
		t.Fatalf("RunAsync: %v", err)
	}

	res := waitContinuation(ctx, store, token, agent.ContinuationCompleted)
	if res == nil {
		t.Fatal("timed out waiting for completion")
	}
	if res.Outcome == nil || !res.Outcome.IsDone() {
		t.Fatalf("outcome = %+v", res.Outcome)
	}
	if res.Outcome.Response.Content != "done" {
		t.Fatalf("content = %q", res.Outcome.Response.Content)
	}
	if len(res.Events) == 0 {
		t.Fatal("expected accumulated events")
	}
}

func TestRunAsync_Paused(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo", Arguments: json.RawMessage(`{"x":1}`)}}},
		{Content: "done"},
	}}
	r := &agent.Runner{Client: fc, Tools: []agent.Tool{gatedEcho()}}
	store := agent.NewMemoryContinuationStore()
	ctx := context.Background()

	token, err := agent.RunAsync(ctx, store, r, llm.Request{Model: "m"})
	if err != nil {
		t.Fatalf("RunAsync: %v", err)
	}

	res := waitContinuation(ctx, store, token, agent.ContinuationPaused)
	if res == nil {
		t.Fatal("timed out waiting for pause")
	}
	if len(res.Requirements) != 1 {
		t.Fatalf("requirements = %+v", res.Requirements)
	}
	if res.Outcome == nil || !res.Outcome.IsPaused() {
		t.Fatalf("outcome = %+v", res.Outcome)
	}
}

func TestContinueAsync_Resumes(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo", Arguments: json.RawMessage(`{"x":1}`)}}},
		{Content: "done"},
	}}
	r := &agent.Runner{Client: fc, Tools: []agent.Tool{gatedEcho()}}
	store := agent.NewMemoryContinuationStore()
	ctx := context.Background()

	runToken, err := agent.RunAsync(ctx, store, r, llm.Request{Model: "m"})
	if err != nil {
		t.Fatalf("RunAsync: %v", err)
	}
	paused := waitContinuation(ctx, store, runToken, agent.ContinuationPaused)
	if paused == nil {
		t.Fatal("timed out waiting for pause")
	}

	contToken, err := agent.ContinueAsync(ctx, store, r, paused.Outcome.RunID, []agent.Resolution{
		{ID: paused.Requirements[0].ID, Approved: true},
	})
	if err != nil {
		t.Fatalf("ContinueAsync: %v", err)
	}

	res := waitContinuation(ctx, store, contToken, agent.ContinuationCompleted)
	if res == nil {
		t.Fatal("timed out waiting for continue completion")
	}
	if res.Outcome == nil || res.Outcome.Response.Content != "done" {
		t.Fatalf("outcome = %+v", res.Outcome)
	}
}

func TestRunAsync_StateTransitions(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{{Content: "hello"}}}
	r := &agent.Runner{Client: fc}
	store := agent.NewMemoryContinuationStore()
	ctx := context.Background()

	token, err := agent.RunAsync(ctx, store, r, llm.Request{Model: "m"})
	if err != nil {
		t.Fatalf("RunAsync: %v", err)
	}

	sawRunning := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		res, err := store.Poll(ctx, token)
		if err != nil {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		switch res.State {
		case agent.ContinuationRunning:
			sawRunning = true
		case agent.ContinuationCompleted:
			if !sawRunning {
				t.Fatal("should observe Running before Completed")
			}
			if res.Outcome == nil || res.Outcome.Response.Content != "hello" {
				t.Fatalf("final outcome = %+v", res.Outcome)
			}
			return
		case agent.ContinuationFailed, agent.ContinuationPaused:
			t.Fatalf("unexpected state %d", res.State)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for state transitions")
}
