package llm_test

import (
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
)

func TestSourceType(t *testing.T) {
	m := llm.Message{Role: llm.User, Content: "hello"}
	if m.Source != llm.SourceUser {
		t.Errorf("default source should be SourceUser, got %d", m.Source)
	}

	m2 := m.WithSource(llm.SourceHistory)
	if m2.Source != llm.SourceHistory {
		t.Errorf("WithSource should set SourceHistory, got %d", m2.Source)
	}
	if m.Source != llm.SourceUser {
		t.Error("WithSource should not modify original")
	}
}

func TestFilterBySource(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.User, Content: "user1", Source: llm.SourceUser},
		{Role: llm.Assistant, Content: "history", Source: llm.SourceHistory},
		{Role: llm.User, Content: "context", Source: llm.SourceContext},
		{Role: llm.Assistant, Content: "model", Source: llm.SourceModel},
		{Role: llm.Tool, Content: "result", Source: llm.SourceTool},
	}

	user := llm.FilterBySource(msgs, llm.SourceUser)
	if len(user) != 1 || user[0].Content != "user1" {
		t.Errorf("FilterBySource(User) = %d, want 1", len(user))
	}

	noHistory := llm.ExcludeSource(msgs, llm.SourceHistory)
	if len(noHistory) != 4 {
		t.Errorf("ExcludeSource(History) = %d, want 4", len(noHistory))
	}
	for _, m := range noHistory {
		if m.Source == llm.SourceHistory {
			t.Error("should not contain history messages")
		}
	}
}

func TestMarkSource(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.User, Content: "a"},
		{Role: llm.Assistant, Content: "b"},
	}
	marked := llm.MarkSource(msgs, llm.SourceContext)
	if len(marked) != 2 {
		t.Fatalf("MarkSource length = %d, want 2", len(marked))
	}
	for _, m := range marked {
		if m.Source != llm.SourceContext {
			t.Errorf("expected SourceContext, got %d", m.Source)
		}
	}
	if msgs[0].Source != llm.SourceUser {
		t.Error("MarkSource should not modify original")
	}
}
