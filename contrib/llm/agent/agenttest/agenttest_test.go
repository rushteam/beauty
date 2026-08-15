package agenttest_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
	"github.com/rushteam/beauty/contrib/llm/agent/agenttest"
)

func TestResponseBuilder_BasicText(t *testing.T) {
	turns := agenttest.NewResponseBuilder().
		AddText("hello").
		Build()

	if len(turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(turns))
	}
	if turns[0].Response == nil || turns[0].Response.Content != "hello" {
		t.Fatalf("response = %+v, want content hello", turns[0].Response)
	}
}

func TestResponseBuilder_BuildDeepClone(t *testing.T) {
	b := agenttest.NewResponseBuilder().AddText("first")
	turns := b.Build()
	b.AddText("mutated")
	if turns[0].Response.Content != "first" {
		t.Fatalf("Build should snapshot content, got %q", turns[0].Response.Content)
	}
}

func TestResponseBuilder_BuildDeepCloneToolCalls(t *testing.T) {
	b := agenttest.NewResponseBuilder().AddToolCall("c1", "echo", `{}`)
	turns := b.Build()
	b.AddToolCall("c2", "other", `{}`)
	if len(turns[0].Response.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(turns[0].Response.ToolCalls))
	}
	if turns[0].Response.ToolCalls[0].ID != "c1" {
		t.Fatalf("tool call = %+v", turns[0].Response.ToolCalls[0])
	}
}

func TestResponseBuilder_MultiTurn(t *testing.T) {
	turns := agenttest.NewResponseBuilder().
		AddText("first").
		NewTurn().
		AddText("second").
		Build()

	if len(turns) != 2 {
		t.Fatalf("turns = %d, want 2", len(turns))
	}
	if turns[0].Response.Content != "first" {
		t.Fatalf("turn 0 content = %q", turns[0].Response.Content)
	}
	if turns[1].Response.Content != "second" {
		t.Fatalf("turn 1 content = %q", turns[1].Response.Content)
	}
}

func TestResponseBuilder_ToolCalls(t *testing.T) {
	turns := agenttest.NewResponseBuilder().
		AddToolCall("c1", "echo", `{"x":1}`).
		NewTurn().
		AddText("done").
		Build()

	if len(turns[0].Response.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(turns[0].Response.ToolCalls))
	}
	tc := turns[0].Response.ToolCalls[0]
	if tc.ID != "c1" || tc.Name != "echo" || string(tc.Arguments) != `{"x":1}` {
		t.Fatalf("tool call = %+v", tc)
	}
	if turns[1].Response.Content != "done" {
		t.Fatalf("turn 1 content = %q", turns[1].Response.Content)
	}
}

func TestScriptedClient_PanicsOnExhaustedTurns(t *testing.T) {
	client := agenttest.NewScriptedClient(agenttest.NewResponseBuilder().
		AddText("only").
		Build())

	_, _ = client.Generate(context.Background(), llm.Request{})

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on exhausted turns")
		} else if got := r.(string); got != "agenttest: no more scripted turns (turn 1)" {
			t.Fatalf("panic = %q, want exhausted turns message", got)
		}
	}()
	client.Generate(context.Background(), llm.Request{})
}

func TestScriptedClient_CallbacksInvoked(t *testing.T) {
	var called int
	var lastReq llm.Request
	turns := agenttest.NewResponseBuilder(func(_ context.Context, req llm.Request) {
		called++
		lastReq = req
	}).
		AddText("ok").
		Build()

	client := agenttest.NewScriptedClient(turns)
	req := llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.User, Content: "hi"}}}
	resp, err := client.Generate(context.Background(), req)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("content = %q", resp.Content)
	}
	if called != 1 {
		t.Fatalf("callback called %d times, want 1", called)
	}
	if lastReq.Model != "m" {
		t.Fatalf("callback req model = %q", lastReq.Model)
	}
}

func TestNewRunner_WorkingAgent(t *testing.T) {
	echo := agent.Func("echo", "回显", nil, func(_ context.Context, args json.RawMessage) (string, error) {
		return "echoed:" + string(args), nil
	})
	turns := agenttest.NewResponseBuilder().
		AddToolCall("c1", "echo", `{"x":1}`).
		NewTurn().
		AddText("done").
		Build()

	r := agenttest.NewRunner(turns, echo)
	out := agent.CollectOutcome(r.Run(context.Background(), llm.Request{
		Model:    "m",
		Messages: []llm.Message{{Role: llm.User, Content: "go"}},
	}))
	resp, err := out.Final()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if resp.Content != "done" {
		t.Fatalf("final content = %q", resp.Content)
	}
}

func TestScriptedClient_Stream(t *testing.T) {
	turns := agenttest.NewResponseBuilder().
		AddText("streamed").
		AddToolCall("c1", "t", `{}`).
		Build()

	client := agenttest.NewScriptedClient(turns)
	resp, err := llm.Collect(client.Stream(context.Background(), llm.Request{}))
	if err != nil {
		t.Fatalf("collect stream: %v", err)
	}
	if resp.Content != "streamed" {
		t.Fatalf("content = %q", resp.Content)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID != "c1" {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
}

func TestScriptedClient_StreamError(t *testing.T) {
	want := errors.New("boom")
	client := agenttest.NewScriptedClient(agenttest.NewResponseBuilder().
		AddError(want).
		Build())

	for _, err := range client.Stream(context.Background(), llm.Request{}) {
		if !errors.Is(err, want) {
			t.Fatalf("stream err = %v, want %v", err, want)
		}
		return
	}
	t.Fatal("expected stream error")
}

func TestScriptedClient_CurrentTurn(t *testing.T) {
	client := agenttest.NewScriptedClient(agenttest.NewResponseBuilder().
		AddText("a").
		NewTurn().
		AddText("b").
		Build())

	if client.CurrentTurn() != 0 {
		t.Fatalf("initial turn = %d, want 0", client.CurrentTurn())
	}
	_, _ = client.Generate(context.Background(), llm.Request{})
	if client.CurrentTurn() != 1 {
		t.Fatalf("after first generate turn = %d, want 1", client.CurrentTurn())
	}
}
