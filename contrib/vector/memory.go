package vector

import (
	"context"
	"sort"
	"sync"
)

// MemoryStore 是纯内存的向量存储:暴力余弦检索。适合开发/测试/小规模语料(几万条内)。
// 大规模用专用向量库实现 Store 接口替换。并发安全。零值不可用,用 NewMemoryStore 构造。
type MemoryStore struct {
	mu   sync.RWMutex
	docs map[string]Document
}

var _ Store = (*MemoryStore)(nil)

// NewMemoryStore 创建内存向量库。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{docs: make(map[string]Document)}
}

// Upsert 插入或更新(按 ID)。向量做防御性拷贝。
func (m *MemoryStore) Upsert(_ context.Context, docs ...Document) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range docs {
		if d.ID == "" {
			continue
		}
		cp := d
		cp.Vector = append([]float32(nil), d.Vector...)
		m.docs[d.ID] = cp
	}
	return nil
}

// Query 返回与 vector 余弦最相似的前 topK 条(降序)。维度不一致的已存项跳过。
func (m *MemoryStore) Query(_ context.Context, vector []float32, topK int) ([]Match, error) {
	if topK <= 0 {
		return nil, nil
	}
	m.mu.RLock()
	matches := make([]Match, 0, len(m.docs))
	for _, d := range m.docs {
		if len(d.Vector) != len(vector) {
			continue // 维度不匹配,跳过(混维语料的健壮处理)
		}
		matches = append(matches, Match{
			ID: d.ID, Score: Cosine(vector, d.Vector), Content: d.Content, Metadata: d.Metadata,
		})
	}
	m.mu.RUnlock()

	sort.Slice(matches, func(i, j int) bool { return matches[i].Score > matches[j].Score })
	if len(matches) > topK {
		matches = matches[:topK]
	}
	return matches, nil
}

// Delete 按 ID 删除。
func (m *MemoryStore) Delete(_ context.Context, ids ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range ids {
		delete(m.docs, id)
	}
	return nil
}

// Len 返回当前存储的向量条数。
func (m *MemoryStore) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.docs)
}
