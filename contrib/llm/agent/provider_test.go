package agent_test

import (
	"context"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

func TestInMemoryHistoryProvider(t *testing.T) {
	hp := agent.NewInMemoryHistoryProvider()
	ctx := context.Background()

	// 首次加载:无历史
	msgs, sysExtra, err := hp.Invoking(ctx, "session-1")
	if err != nil {
		t.Fatalf("Invoking error: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("first invoke: got %d messages, want 0", len(msgs))
	}
	if sysExtra != "" {
		t.Errorf("first invoke: sysExtra = %q, want empty", sysExtra)
	}

	// 写入历史(模拟 agent 完成后)
	err = hp.Invoked(ctx, "session-1", []llm.Message{
		{Role: llm.User, Content: "hello", Source: llm.SourceUser},
		{Role: llm.Assistant, Content: "hi", Source: llm.SourceModel},
		{Role: llm.User, Content: "context info", Source: llm.SourceContext}, // 应被过滤
	})
	if err != nil {
		t.Fatalf("Invoked error: %v", err)
	}

	// 再次加载:应有历史(已排除 SourceContext)
	msgs, _, err = hp.Invoking(ctx, "session-1")
	if err != nil {
		t.Fatalf("Invoking error: %v", err)
	}
	if len(msgs) != 2 {
		t.Errorf("second invoke: got %d messages, want 2 (context excluded)", len(msgs))
	}
	for _, m := range msgs {
		if m.Source != llm.SourceHistory {
			t.Errorf("loaded message should have SourceHistory, got %d", m.Source)
		}
	}
}

func TestRAGContextProvider(t *testing.T) {
	docs := []string{"doc1: Go concurrency", "doc2: channels"}
	cp := agent.RAGContextProvider(func(_ context.Context, query string) ([]string, error) {
		if query != "how do channels work?" {
			t.Errorf("unexpected query: %q", query)
		}
		return docs, nil
	})

	req := &llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "how do channels work?"}},
	}
	msgs, tools, err := cp.Invoking(context.Background(), req)
	if err != nil {
		t.Fatalf("Invoking error: %v", err)
	}
	if len(tools) != 0 {
		t.Errorf("RAG provider should not inject tools, got %d", len(tools))
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 context message, got %d", len(msgs))
	}
	if msgs[0].Source != llm.SourceContext {
		t.Errorf("context message source = %d, want SourceContext", msgs[0].Source)
	}
	if msgs[0].Role != llm.User {
		t.Errorf("context message role = %q, want User", msgs[0].Role)
	}
}

func TestHistoryProviderFunc(t *testing.T) {
	hp := agent.HistoryProviderFunc(func(_ context.Context, id string) ([]llm.Message, string, error) {
		return []llm.Message{{Role: llm.User, Content: "prev"}}, "summary", nil
	})

	msgs, sys, err := hp.Invoking(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Content != "prev" {
		t.Errorf("msgs = %v", msgs)
	}
	if sys != "summary" {
		t.Errorf("sys = %q", sys)
	}

	// Invoked 应该是 no-op
	if err := hp.Invoked(context.Background(), "test", nil); err != nil {
		t.Fatal(err)
	}
}
