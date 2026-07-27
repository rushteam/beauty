package memory_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
	"github.com/rushteam/beauty/contrib/llm/agent/memory"
)

func TestMemoryStore_CRUD(t *testing.T) {
	store := memory.NewMemoryStore()
	ctx := context.Background()
	item := &memory.Item{UserID: "u1", Content: "喜欢喝美式"}
	if err := store.Add(ctx, item); err != nil {
		t.Fatal(err)
	}
	if item.ID == "" {
		t.Fatal("id should be assigned")
	}
	hits, err := store.Search(ctx, "u1", "美式", 5)
	if err != nil || len(hits) != 1 {
		t.Fatalf("search: %v %v", err, hits)
	}
	if err := store.Delete(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	hits, _ = store.Search(ctx, "u1", "美式", 5)
	if len(hits) != 0 {
		t.Fatalf("after delete: %v", hits)
	}
}

func TestMemoryTools_WithRunner(t *testing.T) {
	store := memory.NewMemoryStore()
	tools := memory.Tools(store, "u1")

	fc := &scriptClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "1", Name: "memory_add", Arguments: json.RawMessage(`{"content":"住在上海"}`)}}},
		{ToolCalls: []llm.ToolCall{{ID: "2", Name: "memory_search", Arguments: json.RawMessage(`{"query":"上海"}`)}}},
		{Content: "记得你住上海"},
	}}
	r := &agent.Runner{Client: fc, Tools: tools}
	resp, err := r.Run(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.User, Content: "记一下"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "记得你住上海" {
		t.Fatalf("content=%q", resp.Content)
	}
	// 第二轮 tool 结果应包含记忆内容
	found := false
	for _, m := range fc.seenToolResults {
		if strings.Contains(m, "住在上海") {
			found = true
		}
	}
	if !found {
		t.Fatalf("search results=%v", fc.seenToolResults)
	}
}

type scriptClient struct {
	steps           []*llm.Response
	i               int
	seenToolResults []string
}

func (s *scriptClient) Generate(_ context.Context, req llm.Request) (*llm.Response, error) {
	for _, m := range req.Messages {
		if m.Role == llm.Tool {
			s.seenToolResults = append(s.seenToolResults, m.Content)
		}
	}
	r := s.steps[s.i]
	s.i++
	return r, nil
}

func (s *scriptClient) Stream(context.Context, llm.Request) (<-chan llm.Chunk, error) {
	return nil, nil
}
