# contrib/vector —— 向量存储 / RAG 语义检索(独立模块)

RAG / 语义检索的抽象 `Store` 接口 + 纯内存实现。**纯标准库、零外部依赖**,不 import beauty 核心。
配合 [`contrib/llm`](../llm) 的 `Embedder` 即可搭 RAG。

```bash
go get github.com/rushteam/beauty/contrib/vector@latest
```

## 用法

```go
import "github.com/rushteam/beauty/contrib/vector"

store := vector.NewMemoryStore()
store.Upsert(ctx,
    vector.Document{ID: "1", Vector: emb1, Content: "文档1", Metadata: map[string]string{"src": "faq"}},
    vector.Document{ID: "2", Vector: emb2, Content: "文档2"},
)
hits, _ := store.Query(ctx, queryVec, 5) // 余弦最相似 top5(降序)
for _, h := range hits {
    fmt.Println(h.ID, h.Score, h.Content)
}
```

## 配 contrib/llm 搭 RAG

```go
emb := openai.New(key) // llm.Embedder
// 建索引
vecs, _ := emb.Embed(ctx, chunks)
for i, v := range vecs {
    store.Upsert(ctx, vector.Document{ID: ids[i], Vector: v, Content: chunks[i]})
}
// 检索 + 生成
qv, _ := emb.Embed(ctx, []string{question})
hits, _ := store.Query(ctx, qv[0], 4)
ctxText := join(hits) // 拼上下文
resp, _ := chat.Generate(ctx, llm.Request{Model: "gpt-4o",
    System:   "基于给定资料回答:\n" + ctxText,
    Messages: []llm.Message{{Role: llm.User, Content: question}},
})
```

## 实现

- **`MemoryStore`**:暴力余弦检索,并发安全。适合开发/测试/小规模语料(几万条内)。
- **大规模**:实现同一 `Store` 接口对接专用向量库(pgvector 可架在 [`contrib/sqldb`](../sqldb) 上、
  或 qdrant/milvus)。接口很小(Upsert/Query/Delete),适配成本低。

## 边界

embedding 模型、分块(chunking)、重排(rerank)、混合检索都是 policy。`Cosine(a,b)` 助手可复用。
单测覆盖余弦(同向/正交/零向量/不等长)与 MemoryStore(增查删/降序/更新/混维健壮)。
