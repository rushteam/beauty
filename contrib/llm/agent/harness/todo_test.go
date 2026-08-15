package harness_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
	"github.com/rushteam/beauty/contrib/llm/agent/harness"
)

func TestNewTodoProvider(t *testing.T) {
	p := harness.NewTodoProvider()
	if p == nil {
		t.Fatal("NewTodoProvider returned nil")
	}
	if len(p.GetItems("_default")) != 0 {
		t.Fatalf("new provider should have empty items, got %v", p.GetItems("_default"))
	}
}

func TestTodoProvider_Invoking(t *testing.T) {
	p := harness.NewTodoProvider()
	ctx := context.Background()

	msgs, tools, err := p.Invoking(ctx, &llm.Request{})
	if err != nil {
		t.Fatalf("Invoking error: %v", err)
	}
	if len(tools) != 5 {
		t.Fatalf("expected 5 tools, got %d", len(tools))
	}
	wantTools := []string{"todos_add", "todos_complete", "todos_remove", "todos_get_remaining", "todos_get_all"}
	for i, name := range wantTools {
		if tools[i].Def.Name != name {
			t.Errorf("tool[%d] = %q, want %q", i, tools[i].Def.Name, name)
		}
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 context message, got %d", len(msgs))
	}
	if msgs[0].Source != llm.SourceContext {
		t.Errorf("message source = %d, want SourceContext", msgs[0].Source)
	}
	if msgs[0].Content == "" {
		t.Error("expected non-empty instructions in context message")
	}
}

func TestTodoProvider_AddCompleteRemoveFlow(t *testing.T) {
	p := harness.NewTodoProvider()
	ctx := context.Background()
	_, agentTools, err := p.Invoking(ctx, &llm.Request{})
	if err != nil {
		t.Fatalf("Invoking: %v", err)
	}
	tools := toolMap(t, agentTools)

	addResult, err := tools["todos_add"](ctx, json.RawMessage(`{"title":"第一步","description":"准备环境"}`))
	if err != nil {
		t.Fatalf("todos_add: %v", err)
	}
	if addResult != `ok: added todo #1 "第一步"` {
		t.Fatalf("add result = %q", addResult)
	}

	completeResult, err := tools["todos_complete"](ctx, json.RawMessage(`{"id":1}`))
	if err != nil {
		t.Fatalf("todos_complete: %v", err)
	}
	if completeResult != `ok: completed todo #1 "第一步"` {
		t.Fatalf("complete result = %q", completeResult)
	}

	_, err = tools["todos_add"](ctx, json.RawMessage(`{"title":"第二步"}`))
	if err != nil {
		t.Fatalf("todos_add second: %v", err)
	}

	remainingRaw, err := tools["todos_get_remaining"](ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("todos_get_remaining: %v", err)
	}
	var remaining []harness.TodoItem
	if err := json.Unmarshal([]byte(remainingRaw), &remaining); err != nil {
		t.Fatalf("unmarshal remaining: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != 2 || remaining[0].Title != "第二步" {
		t.Fatalf("remaining = %v", remaining)
	}

	removeResult, err := tools["todos_remove"](ctx, json.RawMessage(`{"id":2}`))
	if err != nil {
		t.Fatalf("todos_remove: %v", err)
	}
	if removeResult != `ok: removed todo #2 "第二步"` {
		t.Fatalf("remove result = %q", removeResult)
	}

	allRaw, err := tools["todos_get_all"](ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("todos_get_all: %v", err)
	}
	var all []harness.TodoItem
	if err := json.Unmarshal([]byte(allRaw), &all); err != nil {
		t.Fatalf("unmarshal all: %v", err)
	}
	if len(all) != 1 || !all[0].IsComplete || all[0].Title != "第一步" {
		t.Fatalf("all = %v", all)
	}
}

func TestTodoProvider_GetItemsGetRemaining(t *testing.T) {
	p := harness.NewTodoProvider()
	ctx := harness.WithTodoSession(context.Background(), "session-a")
	_, agentTools, err := p.Invoking(ctx, &llm.Request{})
	if err != nil {
		t.Fatalf("Invoking: %v", err)
	}
	tools := toolMap(t, agentTools)

	if _, err := tools["todos_add"](ctx, json.RawMessage(`{"title":"A"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := tools["todos_add"](ctx, json.RawMessage(`{"title":"B"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := tools["todos_complete"](ctx, json.RawMessage(`{"id":1}`)); err != nil {
		t.Fatal(err)
	}

	items := p.GetItems("session-a")
	if len(items) != 2 {
		t.Fatalf("GetItems = %d, want 2", len(items))
	}

	remaining := p.GetRemaining("session-a")
	if len(remaining) != 1 || remaining[0].Title != "B" {
		t.Fatalf("GetRemaining = %v", remaining)
	}

	defaultItems := p.GetItems("_default")
	if len(defaultItems) != 0 {
		t.Fatalf("default session should be empty, got %v", defaultItems)
	}
}

func TestTodoProvider_ConcurrentAccess(t *testing.T) {
	p := harness.NewTodoProvider()
	ctx := harness.WithTodoSession(context.Background(), "concurrent")
	_, agentTools, err := p.Invoking(ctx, &llm.Request{})
	if err != nil {
		t.Fatalf("Invoking: %v", err)
	}
	tools := toolMap(t, agentTools)

	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			title, _ := json.Marshal(struct {
				Title string `json:"title"`
			}{Title: "task"})
			if _, err := tools["todos_add"](ctx, json.RawMessage(title)); err != nil {
				t.Errorf("goroutine %d add: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	items := p.GetItems("concurrent")
	if len(items) != n {
		t.Fatalf("expected %d items, got %d", n, len(items))
	}
}

func TestTodoProvider_InvokingIncludesSummary(t *testing.T) {
	p := harness.NewTodoProvider()
	ctx := harness.WithTodoSession(context.Background(), "summary")
	_, agentTools, err := p.Invoking(ctx, &llm.Request{})
	if err != nil {
		t.Fatalf("Invoking: %v", err)
	}
	tools := toolMap(t, agentTools)

	if _, err := tools["todos_add"](ctx, json.RawMessage(`{"title":"写测试"}`)); err != nil {
		t.Fatal(err)
	}

	msgs, _, err := p.Invoking(ctx, &llm.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Source != llm.SourceContext {
		t.Errorf("source = %d, want SourceContext", msgs[0].Source)
	}
	if !containsAll(msgs[0].Content, "当前待办列表", "#1", "写测试", "未完成") {
		t.Fatalf("summary message = %q", msgs[0].Content)
	}
}

func TestTodoProvider_InvokedNoOp(t *testing.T) {
	p := harness.NewTodoProvider()
	if err := p.Invoked(context.Background(), &agent.RunOutcome{}); err != nil {
		t.Fatalf("Invoked: %v", err)
	}
}

func toolMap(t *testing.T, tools []agent.Tool) map[string]func(context.Context, json.RawMessage) (string, error) {
	t.Helper()
	m := make(map[string]func(context.Context, json.RawMessage) (string, error), len(tools))
	for _, tool := range tools {
		m[tool.Def.Name] = tool.Call
	}
	return m
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}
