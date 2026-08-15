// Package harness 提供 agent 运行时的上下文注入器(ContextProvider)。
package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

const (
	defaultSessionID    = "_default"
	defaultStateKey     = "todo"
	defaultInstructions = "你有一组待办事项管理工具。在处理复杂任务时,请先用 todos_add 分解任务," +
		"完成每步后用 todos_complete 标记,随时用 todos_get_remaining 检查进度。"
)

type todoSessionKey struct{}

// WithTodoSession 在 context 中设置 todo session ID。
func WithTodoSession(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, todoSessionKey{}, sessionID)
}

func todoSessionFrom(ctx context.Context) string {
	if v, ok := ctx.Value(todoSessionKey{}).(string); ok && v != "" {
		return v
	}
	return defaultSessionID
}

// TodoItem 是一个待办事项。
type TodoItem struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	IsComplete  bool   `json:"is_complete"`
}

// TodoProvider 是一个 ContextProvider,在每次 agent 运行时注入待办事项管理工具。
// 待办列表通过 stateKey 持久化(默认在内存中)。
type TodoProvider struct {
	Instructions string
	stateKey     string
	mu           sync.Mutex
	items        map[string][]TodoItem // key by session/context ID
	nextID       map[string]int
}

// NewTodoProvider 创建 todo harness。
func NewTodoProvider() *TodoProvider {
	return &TodoProvider{
		Instructions: defaultInstructions,
		stateKey:     defaultStateKey,
		items:        make(map[string][]TodoItem),
		nextID:       make(map[string]int),
	}
}

// GetItems 获取所有待办事项(外部访问用)。
func (p *TodoProvider) GetItems(sessionID string) []TodoItem {
	p.mu.Lock()
	defer p.mu.Unlock()
	return cloneItems(p.items[sessionID])
}

// GetRemaining 获取未完成的待办事项。
func (p *TodoProvider) GetRemaining(sessionID string) []TodoItem {
	p.mu.Lock()
	defer p.mu.Unlock()
	all := p.items[sessionID]
	out := make([]TodoItem, 0, len(all))
	for _, it := range all {
		if !it.IsComplete {
			out = append(out, it)
		}
	}
	return out
}

// Invoking implements ContextProvider。
func (p *TodoProvider) Invoking(ctx context.Context, _ *llm.Request) ([]llm.Message, []agent.Tool, error) {
	sessionID := todoSessionFrom(ctx)

	p.mu.Lock()
	summary := formatTodoSummary(p.items[sessionID])
	p.mu.Unlock()

	content := p.Instructions
	if summary != "" {
		content += "\n\n" + summary
	}

	msgs := []llm.Message{{
		Role:    llm.User,
		Content: content,
		Source:  llm.SourceContext,
	}}
	return msgs, p.tools(), nil
}

// Invoked implements ContextProvider。
func (p *TodoProvider) Invoked(_ context.Context, _ *agent.RunOutcome) error {
	return nil
}

func (p *TodoProvider) tools() []agent.Tool {
	return []agent.Tool{
		agent.Func("todos_add", "添加一条待办事项", json.RawMessage(`{
			"type":"object",
			"properties":{
				"title":{"type":"string"},
				"description":{"type":"string"}
			},
			"required":["title"]
		}`), p.toolAdd),
		agent.Func("todos_complete", "将待办事项标记为已完成", json.RawMessage(`{
			"type":"object",
			"properties":{"id":{"type":"integer"}},
			"required":["id"]
		}`), p.toolComplete),
		agent.Func("todos_remove", "删除一条待办事项", json.RawMessage(`{
			"type":"object",
			"properties":{"id":{"type":"integer"}},
			"required":["id"]
		}`), p.toolRemove),
		agent.Func("todos_get_remaining", "获取所有未完成的待办事项", json.RawMessage(`{
			"type":"object",
			"properties":{}
		}`), p.toolGetRemaining),
		agent.Func("todos_get_all", "获取全部待办事项", json.RawMessage(`{
			"type":"object",
			"properties":{}
		}`), p.toolGetAll),
	}
}

func (p *TodoProvider) toolAdd(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("harness: unmarshal todos_add args: %w", err)
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return "", fmt.Errorf("harness: title is required")
	}

	sessionID := todoSessionFrom(ctx)
	p.mu.Lock()
	defer p.mu.Unlock()

	p.nextID[sessionID]++
	id := p.nextID[sessionID]
	item := TodoItem{ID: id, Title: title, Description: strings.TrimSpace(in.Description)}
	p.items[sessionID] = append(p.items[sessionID], item)
	return fmt.Sprintf("ok: added todo #%d %q", id, title), nil
}

func (p *TodoProvider) toolComplete(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("harness: unmarshal todos_complete args: %w", err)
	}
	if in.ID <= 0 {
		return "", fmt.Errorf("harness: invalid id %d", in.ID)
	}

	sessionID := todoSessionFrom(ctx)
	p.mu.Lock()
	defer p.mu.Unlock()

	items := p.items[sessionID]
	for i := range items {
		if items[i].ID == in.ID {
			if items[i].IsComplete {
				return fmt.Sprintf("ok: todo #%d already complete", in.ID), nil
			}
			items[i].IsComplete = true
			p.items[sessionID] = items
			return fmt.Sprintf("ok: completed todo #%d %q", in.ID, items[i].Title), nil
		}
	}
	return "", fmt.Errorf("harness: todo #%d not found", in.ID)
}

func (p *TodoProvider) toolRemove(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("harness: unmarshal todos_remove args: %w", err)
	}
	if in.ID <= 0 {
		return "", fmt.Errorf("harness: invalid id %d", in.ID)
	}

	sessionID := todoSessionFrom(ctx)
	p.mu.Lock()
	defer p.mu.Unlock()

	items := p.items[sessionID]
	for i := range items {
		if items[i].ID == in.ID {
			title := items[i].Title
			p.items[sessionID] = append(items[:i], items[i+1:]...)
			return fmt.Sprintf("ok: removed todo #%d %q", in.ID, title), nil
		}
	}
	return "", fmt.Errorf("harness: todo #%d not found", in.ID)
}

func (p *TodoProvider) toolGetRemaining(ctx context.Context, _ json.RawMessage) (string, error) {
	sessionID := todoSessionFrom(ctx)
	p.mu.Lock()
	defer p.mu.Unlock()

	all := p.items[sessionID]
	out := make([]TodoItem, 0, len(all))
	for _, it := range all {
		if !it.IsComplete {
			out = append(out, it)
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("harness: marshal remaining todos: %w", err)
	}
	return string(b), nil
}

func (p *TodoProvider) toolGetAll(ctx context.Context, _ json.RawMessage) (string, error) {
	sessionID := todoSessionFrom(ctx)
	p.mu.Lock()
	defer p.mu.Unlock()

	b, err := json.Marshal(cloneItems(p.items[sessionID]))
	if err != nil {
		return "", fmt.Errorf("harness: marshal todos: %w", err)
	}
	return string(b), nil
}

func formatTodoSummary(items []TodoItem) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("当前待办列表:\n")
	for _, it := range items {
		status := "未完成"
		if it.IsComplete {
			status = "已完成"
		}
		fmt.Fprintf(&b, "- #%d [%s] %s", it.ID, status, it.Title)
		if it.Description != "" {
			fmt.Fprintf(&b, " — %s", it.Description)
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

func cloneItems(items []TodoItem) []TodoItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]TodoItem, len(items))
	copy(out, items)
	return out
}
