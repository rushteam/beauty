package llm_test

import (
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
)

func sampleMessages() []llm.Message {
	return []llm.Message{
		{Role: llm.User, Content: "user1", Source: llm.SourceUser},
		{Role: llm.Assistant, Content: "history", Source: llm.SourceHistory},
		{Role: llm.User, Content: "context", Source: llm.SourceContext},
		{Role: llm.Assistant, Content: "model", Source: llm.SourceModel},
		{Role: llm.Tool, Content: "result", Source: llm.SourceTool},
		{Role: llm.Assistant, Content: "", Source: llm.SourceModel, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "search"}}},
		{Role: llm.Assistant, Content: "middleware", Source: llm.SourceMiddleware},
		{Role: llm.User, Content: "", Source: llm.SourceUser},
	}
}

func TestFilterBySource_composable(t *testing.T) {
	msgs := sampleMessages()
	got := llm.BySource(llm.SourceUser, llm.SourceModel).Apply(msgs)
	if len(got) != 4 {
		t.Fatalf("BySource(User, Model).Apply = %d messages, want 4", len(got))
	}
	for _, m := range got {
		if m.Source != llm.SourceUser && m.Source != llm.SourceModel {
			t.Errorf("unexpected source %d in %q", m.Source, m.Content)
		}
	}
}

func TestFilterByRole(t *testing.T) {
	msgs := sampleMessages()
	got := llm.ByRole(llm.User, llm.Assistant).Apply(msgs)
	if len(got) != 7 {
		t.Fatalf("ByRole(User, Assistant).Apply = %d messages, want 7", len(got))
	}
	for _, m := range got {
		if m.Role != llm.User && m.Role != llm.Assistant {
			t.Errorf("unexpected role %q in %q", m.Role, m.Content)
		}
	}
}

func TestFilterAnd(t *testing.T) {
	msgs := sampleMessages()
	got := llm.And(llm.BySource(llm.SourceUser), llm.HasContent()).Apply(msgs)
	if len(got) != 1 {
		t.Fatalf("And(BySource(User), HasContent()).Apply = %d messages, want 1", len(got))
	}
	if got[0].Content != "user1" {
		t.Errorf("got content %q, want user1", got[0].Content)
	}
}

func TestFilterOr(t *testing.T) {
	msgs := sampleMessages()
	got := llm.Or(llm.BySource(llm.SourceUser), llm.BySource(llm.SourceModel)).Apply(msgs)
	if len(got) != 4 {
		t.Fatalf("Or(BySource(User), BySource(Model)).Apply = %d messages, want 4", len(got))
	}
}

func TestFilterNot(t *testing.T) {
	msgs := sampleMessages()
	got := llm.Not(llm.BySource(llm.SourceHistory)).Apply(msgs)
	if len(got) != len(msgs)-1 {
		t.Fatalf("Not(BySource(History)).Apply = %d messages, want %d", len(got), len(msgs)-1)
	}
	for _, m := range got {
		if m.Source == llm.SourceHistory {
			t.Error("should not contain history messages")
		}
	}
}

func TestFilterPersistable(t *testing.T) {
	msgs := sampleMessages()
	got := llm.Persistable().Apply(msgs)
	if len(got) != 5 {
		t.Fatalf("Persistable().Apply = %d messages, want 5", len(got))
	}
	for _, m := range got {
		switch m.Source {
		case llm.SourceHistory, llm.SourceContext, llm.SourceMiddleware:
			t.Errorf("persistable filter should exclude source %d (%q)", m.Source, m.Content)
		}
	}
}

func TestFilterApplyNilFilter(t *testing.T) {
	msgs := sampleMessages()
	got := (llm.MessageFilter)(nil).Apply(msgs)
	if len(got) != len(msgs) {
		t.Fatalf("nil filter Apply = %d messages, want %d (passthrough)", len(got), len(msgs))
	}
	for i := range msgs {
		if got[i].Content != msgs[i].Content || got[i].Source != msgs[i].Source {
			t.Errorf("message[%d] changed after nil filter", i)
		}
	}
}

func TestFilterApplyEmpty(t *testing.T) {
	got := llm.BySource(llm.SourceUser).Apply(nil)
	if len(got) != 0 {
		t.Fatalf("Apply(nil) = %d messages, want 0", len(got))
	}
	got = llm.BySource(llm.SourceUser).Apply([]llm.Message{})
	if len(got) != 0 {
		t.Fatalf("Apply(empty) = %d messages, want 0", len(got))
	}
}

func TestFilterComposition(t *testing.T) {
	msgs := sampleMessages()
	filter := llm.And(
		llm.ExcludeSources(llm.SourceHistory, llm.SourceContext),
		llm.Or(llm.ByRole(llm.User), llm.ByRole(llm.Assistant)),
	)
	got := filter.Apply(msgs)
	if len(got) != 5 {
		t.Fatalf("complex composition = %d messages, want 5", len(got))
	}
	for _, m := range got {
		if m.Source == llm.SourceHistory || m.Source == llm.SourceContext {
			t.Errorf("should exclude history/context, got source %d", m.Source)
		}
		if m.Role != llm.User && m.Role != llm.Assistant {
			t.Errorf("should only keep user/assistant roles, got %q", m.Role)
		}
	}
}

func TestHasToolCalls(t *testing.T) {
	msgs := sampleMessages()
	got := llm.HasToolCalls().Apply(msgs)
	if len(got) != 1 {
		t.Fatalf("HasToolCalls().Apply = %d messages, want 1", len(got))
	}
	if len(got[0].ToolCalls) == 0 {
		t.Error("expected message with tool calls")
	}
}
