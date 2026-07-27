# contrib/memoryvector —— 语义长期记忆

把 `llm.Embedder` + [`contrib/vector`](../vector) 接到 [`llm/agent/memory.Store`](../llm/agent/memory),
供 `memory.Tools` 做语义召回(相对内存版的子串匹配)。

```bash
go get github.com/rushteam/beauty/contrib/memoryvector@latest
```

```go
emb := openai.New(os.Getenv("OPENAI_API_KEY")) // 实现 llm.Embedder
vs := vector.NewMemoryStore()                  // 或自建 pgvector 等
store, _ := memoryvector.New(emb, vs)

r := &agent.Runner{Client: cli, Tools: memory.Tools(store, "user-42")}
```
