// Package vector 是 beauty 的向量存储薄机制:RAG / 语义检索的抽象 Store 接口 + 一个纯内存
// 实现。作为**独立 Go 模块**发布(github.com/rushteam/beauty/contrib/vector),**纯标准库、
// 零外部依赖**,不 import beauty 核心。配合 contrib/llm 的 Embedder 即可搭 RAG:
// 文本 → Embed(llm)→ Upsert/Query(vector)→ 拼上下文 → Generate(llm)。
//
// 内置 MemoryStore(暴力余弦,适合开发/测试/小规模语料);生产大规模用专用向量库
// (pgvector / qdrant / milvus)实现同一 Store 接口即可——接口小、易适配。
//
// 边界(机制而非策略):用哪个 embedding 模型、分块(chunking)策略、重排(rerank)、
// 混合检索都在使用方。
package vector

import (
	"context"
	"errors"
	"math"
)

// Document 是一条待存储的向量记录。Vector 必填;Content/Metadata 可空(查询时原样带回)。
type Document struct {
	ID       string
	Vector   []float32
	Content  string
	Metadata map[string]string
}

// Match 是一次查询的命中项,按相似度降序。
type Match struct {
	ID       string
	Score    float32 // 相似度(余弦,越大越相似)
	Content  string
	Metadata map[string]string
}

// Store 是向量存储接口。实现应保证并发安全。
type Store interface {
	// Upsert 插入或更新若干向量(按 ID)。
	Upsert(ctx context.Context, docs ...Document) error
	// Query 返回与 vector 最相似的前 topK 条(降序)。
	Query(ctx context.Context, vector []float32, topK int) ([]Match, error)
	// Delete 按 ID 删除。
	Delete(ctx context.Context, ids ...string) error
}

// ErrDimMismatch 表示查询向量与已存向量维度不一致。
var ErrDimMismatch = errors.New("vector: dimension mismatch")

// Cosine 计算两个等长向量的余弦相似度([-1,1];任一为零向量返回 0)。
func Cosine(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(na) * math.Sqrt(nb)))
}
