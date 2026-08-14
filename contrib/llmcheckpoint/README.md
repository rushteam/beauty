# llmcheckpoint — Agent Run 持久化

`contrib/llm/agent` 的 `RunStore` / `CheckpointStore` SQLite 与 Redis 实现。

```go
import "github.com/rushteam/beauty/contrib/llmcheckpoint"

store, err := llmcheckpoint.NewSQLite("checkpoints.db")
// 或
rstore, err := llmcheckpoint.NewRedis(redisClient, "llm:run:")
```

配合 `contrib/llm/agent/httpui` SSE handler 做 HITL 回放。

```bash
cd contrib/llmcheckpoint && go test ./...
```
