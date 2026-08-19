package a2a

import (
	"context"
	"iter"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

type mockAgent struct {
	events []agent.Event
	info   agent.Info
}

func (m *mockAgent) Run(_ context.Context, _ llm.Request, _ ...agent.Option) iter.Seq2[agent.Event, error] {
	return func(yield func(agent.Event, error) bool) {
		for _, ev := range m.events {
			if !yield(ev, nil) {
				return
			}
		}
	}
}

func (m *mockAgent) Continue(_ context.Context, _ string, _ []agent.Resolution, _ ...agent.Option) iter.Seq2[agent.Event, error] {
	return func(yield func(agent.Event, error) bool) {}
}

func (m *mockAgent) Info() agent.Info {
	if m.info.Name != "" {
		return m.info
	}
	return agent.Info{Name: "mock-agent"}
}

func TestMessagesToParts_TextContent(t *testing.T) {
	parts := messagesToParts([]llm.Message{{Role: llm.User, Content: "hello"}})
	if len(parts) != 1 {
		t.Fatalf("len(parts) = %d, want 1", len(parts))
	}
	if parts[0].Text() != "hello" {
		t.Fatalf("text = %q", parts[0].Text())
	}
}

func TestPartsToMessage_Text(t *testing.T) {
	msg := partsToMessage(a2a.ContentParts{a2a.NewTextPart("hi"), a2a.NewTextPart("there")}, llm.Assistant)
	if msg.Role != llm.Assistant {
		t.Fatalf("role = %v", msg.Role)
	}
	if msg.Content != "hi\nthere" {
		t.Fatalf("content = %q", msg.Content)
	}
}

func TestA2ARoleToBeauty(t *testing.T) {
	if got := a2aRoleToBeauty(a2a.MessageRoleAgent); got != llm.Assistant {
		t.Fatalf("agent role = %v, want assistant", got)
	}
	if got := a2aRoleToBeauty(a2a.MessageRoleUser); got != llm.User {
		t.Fatalf("user role = %v", got)
	}
}

func TestNewExecutor_NilMessage(t *testing.T) {
	exec := NewExecutor(&mockAgent{}, ServerConfig{})
	ctx := context.Background()
	var gotErr error
	for ev, err := range exec.Execute(ctx, &a2asrv.ExecutorContext{}) {
		if ev != nil {
			t.Fatal("expected nil event for nil message")
		}
		gotErr = err
	}
	if gotErr == nil {
		t.Fatal("expected error for nil message")
	}
	if gotErr.Error() != "a2a server: nil message" {
		t.Fatalf("error = %q", gotErr.Error())
	}
}

func TestExecutorExecute_TokenAndFinal(t *testing.T) {
	exec := NewExecutor(&mockAgent{
		events: []agent.Event{
			{Type: agent.EventToken, Response: &llm.Response{Content: "hel"}},
			{Type: agent.EventToken, Response: &llm.Response{Content: "lo"}},
			{Type: agent.EventFinal},
		},
	}, ServerConfig{})

	ctx := context.Background()
	execCtx := &a2asrv.ExecutorContext{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hi")),
	}

	var (
		submitted bool
		working   bool
		completed bool
		artifacts int
	)
	for ev, err := range exec.Execute(ctx, execCtx) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		switch e := ev.(type) {
		case *a2a.TaskStatusUpdateEvent:
			switch e.Status.State {
			case a2a.TaskStateSubmitted:
				submitted = true
			case a2a.TaskStateWorking:
				working = true
			case a2a.TaskStateCompleted:
				completed = true
			}
		case *a2a.TaskArtifactUpdateEvent:
			artifacts++
		}
	}
	if !submitted || !working || !completed {
		t.Fatalf("states submitted=%v working=%v completed=%v", submitted, working, completed)
	}
	if artifacts < 1 {
		t.Fatal("expected at least one artifact event")
	}
}

func TestExecutorCancel(t *testing.T) {
	exec := NewExecutor(&mockAgent{}, ServerConfig{})
	ctx := context.Background()
	execCtx := &a2asrv.ExecutorContext{}

	var canceled bool
	for ev, err := range exec.Cancel(ctx, execCtx) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if e, ok := ev.(*a2a.TaskStatusUpdateEvent); ok && e.Status.State == a2a.TaskStateCanceled {
			canceled = true
		}
	}
	if !canceled {
		t.Fatal("expected canceled status event")
	}
}

func TestNewAgent_Info(t *testing.T) {
	a := &remoteAgent{cfg: ClientConfig{Name: "remote", Description: "desc"}}
	info := a.Info()
	if info.Name != "remote" || info.Description != "desc" {
		t.Fatalf("Info() = %+v", info)
	}
}

func TestEmitOutcome_Done(t *testing.T) {
	var got agent.Event
	emitOutcome(func(ev agent.Event, err error) bool {
		got = ev
		return true
	}, agent.RunOutcome{
		Status:   agent.StatusDone,
		RunID:    "run-1",
		Response: &llm.Response{Content: "ok"},
	})
	if got.Type != agent.EventFinal {
		t.Fatalf("event type = %v", got.Type)
	}
	if got.Response.Content != "ok" {
		t.Fatalf("content = %q", got.Response.Content)
	}
}
