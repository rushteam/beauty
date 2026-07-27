// Package memory 给 agent 提供**跨会话**的薄长期记忆:Store 接口 + 内存实现 +
// 可直接挂到 Runner 的工具(memory_add / memory_search / memory_delete)。
//
// 与 session 包的区别:session 管单次对话历史;memory 管用户级事实/笔记,可被工具读写。
// 检索默认是子串匹配(零依赖);语义检索用 contrib/memoryvector(Embedder + contrib/vector)。
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rushteam/beauty/contrib/llm/agent"
)

// Item 是一条记忆。
type Item struct {
	ID        string            `json:"id"`
	UserID    string            `json:"user_id,omitempty"`
	Content   string            `json:"content"`
	Meta      map[string]string `json:"meta,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
}

// Store 持久化记忆。Search 按实现定义相关性(内存版=子串包含)。
type Store interface {
	Add(ctx context.Context, item *Item) error
	Search(ctx context.Context, userID, query string, topK int) ([]Item, error)
	Delete(ctx context.Context, ids ...string) error
}

// MemoryStore 并发安全的内存实现(子串检索,不区分大小写)。
type MemoryStore struct {
	mu    sync.RWMutex
	items map[string]Item
	seq   int
}

// NewMemoryStore 创建内存记忆库。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{items: map[string]Item{}}
}

// Add 写入一条记忆;ID 为空则自动生成并写回 item.ID。
func (m *MemoryStore) Add(_ context.Context, item *Item) error {
	if item == nil {
		return fmt.Errorf("memory: nil item")
	}
	if strings.TrimSpace(item.Content) == "" {
		return fmt.Errorf("memory: empty content")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if item.ID == "" {
		m.seq++
		item.ID = fmt.Sprintf("m%d", m.seq)
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now()
	}
	cp := *item
	if item.Meta != nil {
		cp.Meta = make(map[string]string, len(item.Meta))
		for k, v := range item.Meta {
			cp.Meta[k] = v
		}
	}
	m.items[cp.ID] = cp
	return nil
}

// Search 返回 Content 包含 query 的条目(userID 非空则过滤);query 空则返回最多 topK 条。
func (m *MemoryStore) Search(_ context.Context, userID, query string, topK int) ([]Item, error) {
	if topK <= 0 {
		topK = 5
	}
	q := strings.ToLower(strings.TrimSpace(query))
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Item, 0)
	for _, it := range m.items {
		if userID != "" && it.UserID != userID {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(it.Content), q) {
			continue
		}
		out = append(out, it)
		if len(out) >= topK {
			break
		}
	}
	return out, nil
}

// Delete 按 ID 删除。
func (m *MemoryStore) Delete(_ context.Context, ids ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range ids {
		delete(m.items, id)
	}
	return nil
}

// Tools 返回 memory_add / memory_search / memory_delete,可直接 append 到 Runner.Tools。
// userID 写入每条记忆的 UserID(搜索时默认按该用户过滤);空则不按用户过滤。
func Tools(store Store, userID string) []agent.Tool {
	return []agent.Tool{
		agent.Func("memory_add", "把一条事实/笔记写入长期记忆", json.RawMessage(`{
			"type":"object",
			"properties":{"content":{"type":"string"},"id":{"type":"string","description":"可选自定义 ID"}},
			"required":["content"]
		}`), func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Content string `json:"content"`
				ID      string `json:"id"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			item := &Item{ID: in.ID, UserID: userID, Content: in.Content}
			if err := store.Add(ctx, item); err != nil {
				return "", err
			}
			return fmt.Sprintf("ok: remembered %q", item.ID), nil
		}),
		agent.Func("memory_search", "从长期记忆中检索相关条目", json.RawMessage(`{
			"type":"object",
			"properties":{
				"query":{"type":"string"},
				"top_k":{"type":"integer","description":"默认 5"}
			},
			"required":["query"]
		}`), func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Query string `json:"query"`
				TopK  int    `json:"top_k"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			hits, err := store.Search(ctx, userID, in.Query, in.TopK)
			if err != nil {
				return "", err
			}
			if len(hits) == 0 {
				return "no memories found", nil
			}
			b, _ := json.Marshal(hits)
			return string(b), nil
		}),
		agent.Func("memory_delete", "按 ID 删除长期记忆", json.RawMessage(`{
			"type":"object",
			"properties":{"ids":{"type":"array","items":{"type":"string"}}},
			"required":["ids"]
		}`), func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				IDs []string `json:"ids"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return "", err
			}
			if err := store.Delete(ctx, in.IDs...); err != nil {
				return "", err
			}
			return fmt.Sprintf("ok: deleted %d", len(in.IDs)), nil
		}),
	}
}
