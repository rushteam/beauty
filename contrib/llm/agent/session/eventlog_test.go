package session_test

import (
	"context"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent/session"
)

func TestStoreEvents(t *testing.T) {
	store := session.NewMemoryStore()
	ctx := context.Background()

	events := []session.SessionEvent{
		{Kind: session.EventUserMessage, Message: &llm.Message{Role: llm.User, Content: "hello"}},
		{Kind: session.EventAssistantMessage, Message: &llm.Message{Role: llm.Assistant, Content: "hi"}},
		{Kind: session.EventToolCall, Message: &llm.Message{Role: llm.Assistant, Content: "", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "search"}}}},
		{Kind: session.EventToolResult, ToolCall: &llm.ToolCall{ID: "c1"}, Result: `{"data":42}`},
	}

	if err := store.AppendEvents(ctx, "sess1", events...); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.LoadEvents(ctx, "sess1")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 4 {
		t.Fatalf("expected 4 events, got %d", len(loaded))
	}
	if loaded[0].Timestamp.IsZero() {
		t.Error("AppendEvents should set timestamp")
	}
}

func TestReplay(t *testing.T) {
	events := []session.SessionEvent{
		{Kind: session.EventUserMessage, Message: &llm.Message{Role: llm.User, Content: "hello"}},
		{Kind: session.EventAssistantMessage, Message: &llm.Message{Role: llm.Assistant, Content: "hi"}},
		{Kind: session.EventSummaryGenerated, Result: "用户打了招呼"},
		{Kind: session.EventUserMessage, Message: &llm.Message{Role: llm.User, Content: "how are you?"}},
		{Kind: session.EventAssistantMessage, Message: &llm.Message{Role: llm.Assistant, Content: "fine"}},
	}

	summary, msgs := session.Replay(events)

	if summary != "用户打了招呼" {
		t.Errorf("summary = %q", summary)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages after summary, got %d", len(msgs))
	}
	if msgs[0].Content != "how are you?" {
		t.Errorf("msgs[0] = %q", msgs[0].Content)
	}
}

func TestFork(t *testing.T) {
	store := session.NewMemoryStore()
	ctx := context.Background()

	events := []session.SessionEvent{
		{Kind: session.EventUserMessage, Message: &llm.Message{Role: llm.User, Content: "a"}},
		{Kind: session.EventAssistantMessage, Message: &llm.Message{Role: llm.Assistant, Content: "b"}},
		{Kind: session.EventUserMessage, Message: &llm.Message{Role: llm.User, Content: "c"}},
		{Kind: session.EventAssistantMessage, Message: &llm.Message{Role: llm.Assistant, Content: "d"}},
	}
	_ = store.AppendEvents(ctx, "source", events...)

	if err := session.Fork(ctx, store, "source", "forked", 1); err != nil {
		t.Fatal(err)
	}

	forked, _ := store.LoadEvents(ctx, "forked")
	if len(forked) != 3 {
		t.Fatalf("expected 3 events (2 copied + 1 fork marker), got %d", len(forked))
	}
	if forked[2].Kind != session.EventSessionForked {
		t.Errorf("last event should be fork marker, got %s", forked[2].Kind)
	}
	if forked[2].ForkFrom != "source" {
		t.Errorf("fork_from = %q", forked[2].ForkFrom)
	}
}

func TestRecordMessages(t *testing.T) {
	store := session.NewMemoryStore()
	ctx := context.Background()

	msgs := []llm.Message{
		{Role: llm.User, Content: "hi"},
		{Role: llm.Assistant, Content: "hello"},
	}

	if err := session.RecordMessages(ctx, store, "s1", 1, "run-1", msgs); err != nil {
		t.Fatal(err)
	}

	events, _ := store.LoadEvents(ctx, "s1")
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Kind != session.EventUserMessage {
		t.Errorf("events[0].Kind = %s", events[0].Kind)
	}
	if events[1].Kind != session.EventAssistantMessage {
		t.Errorf("events[1].Kind = %s", events[1].Kind)
	}
}

func TestReplayToSession(t *testing.T) {
	events := []session.SessionEvent{
		{Kind: session.EventUserMessage, Message: &llm.Message{Role: llm.User, Content: "hi"}},
		{Kind: session.EventAssistantMessage, Message: &llm.Message{Role: llm.Assistant, Content: "hello"}},
	}

	sess := session.ReplayToSession("test-id", events)
	if sess.ID != "test-id" {
		t.Errorf("id = %q", sess.ID)
	}
	if len(sess.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(sess.Messages))
	}
}
