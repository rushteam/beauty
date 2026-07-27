// Package memoryvector 把 contrib/vector + llm.Embedder 接到 agent/memory.Store,
// 提供语义检索的长期记忆(Add 时 embedding,Search 时向量召回)。
package memoryvector

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent/memory"
	"github.com/rushteam/beauty/contrib/vector"
)

// Store 实现 memory.Store:内容经 Embedder 向量化后写入 vector.Store。
type Store struct {
	emb  llm.Embedder
	vs   vector.Store
	topK int

	mu  sync.Mutex
	seq int
}

// Option 配置 Store。
type Option func(*Store)

// WithDefaultTopK 未指定 topK 时的默认召回数(默认 5)。
func WithDefaultTopK(n int) Option {
	return func(s *Store) {
		if n > 0 {
			s.topK = n
		}
	}
}

// New 创建语义记忆库。emb / vs 不可为 nil。
func New(emb llm.Embedder, vs vector.Store, opts ...Option) (*Store, error) {
	if emb == nil {
		return nil, fmt.Errorf("memoryvector: nil embedder")
	}
	if vs == nil {
		return nil, fmt.Errorf("memoryvector: nil vector store")
	}
	s := &Store{emb: emb, vs: vs, topK: 5}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// Add 实现 memory.Store:embedding 后 Upsert;ID 空则自动生成。
func (s *Store) Add(ctx context.Context, item *memory.Item) error {
	if item == nil {
		return fmt.Errorf("memoryvector: nil item")
	}
	if item.Content == "" {
		return fmt.Errorf("memoryvector: empty content")
	}
	if item.ID == "" {
		s.mu.Lock()
		s.seq++
		n := s.seq
		s.mu.Unlock()
		item.ID = "mv" + strconv.Itoa(n) + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	vecs, err := s.emb.Embed(ctx, []string{item.Content})
	if err != nil {
		return fmt.Errorf("memoryvector: embed: %w", err)
	}
	if len(vecs) == 0 || len(vecs[0]) == 0 {
		return fmt.Errorf("memoryvector: empty embedding")
	}
	meta := map[string]string{}
	for k, v := range item.Meta {
		meta[k] = v
	}
	if item.UserID != "" {
		meta["user_id"] = item.UserID
	}
	meta["created_at"] = item.CreatedAt.Format(time.RFC3339Nano)
	return s.vs.Upsert(ctx, vector.Document{
		ID:       item.ID,
		Vector:   vecs[0],
		Content:  item.Content,
		Metadata: meta,
	})
}

// Search 实现 memory.Store:query embedding 后向量召回;userID 非空则过滤 metadata。
func (s *Store) Search(ctx context.Context, userID, query string, topK int) ([]memory.Item, error) {
	if topK <= 0 {
		topK = s.topK
	}
	// 多取一些再按 user 过滤
	fetch := topK
	if userID != "" {
		fetch = topK * 4
		if fetch < 20 {
			fetch = 20
		}
	}
	vecs, err := s.emb.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("memoryvector: embed query: %w", err)
	}
	if len(vecs) == 0 {
		return nil, nil
	}
	hits, err := s.vs.Query(ctx, vecs[0], fetch)
	if err != nil {
		return nil, err
	}
	out := make([]memory.Item, 0, topK)
	for _, h := range hits {
		uid := ""
		if h.Metadata != nil {
			uid = h.Metadata["user_id"]
		}
		if userID != "" && uid != userID {
			continue
		}
		meta := map[string]string{}
		var created time.Time
		for k, v := range h.Metadata {
			if k == "user_id" {
				continue
			}
			if k == "created_at" {
				created, _ = time.Parse(time.RFC3339Nano, v)
				continue
			}
			meta[k] = v
		}
		out = append(out, memory.Item{
			ID:        h.ID,
			UserID:    uid,
			Content:   h.Content,
			Meta:      meta,
			CreatedAt: created,
		})
		if len(out) >= topK {
			break
		}
	}
	return out, nil
}

// Delete 实现 memory.Store。
func (s *Store) Delete(ctx context.Context, ids ...string) error {
	return s.vs.Delete(ctx, ids...)
}

var _ memory.Store = (*Store)(nil)
