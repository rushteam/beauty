# llmcheckpoint — Agent Run 持久化

`contrib/llm/agent` 的 `RunStore` / `CheckpointStore` SQLite 与 Redis 实现，同时实现
`RunCoordinator`：多副本 `Continue` 会为同一 `runID` 申请可续约 lease，避免重复执行审批后的工具。

```go
import "github.com/rushteam/beauty/contrib/llmcheckpoint"

store, err := llmcheckpoint.NewSQLite("checkpoints.db")
// 或
rstore, err := llmcheckpoint.NewRedis(redisClient, "llm:run:")
```

配合 `contrib/llm/agent/httpui` SSE handler 做 HITL 回放。

## 生产建议

- **SQLite**：适合单实例或单个持久卷。它不跨节点共享数据库文件；高可用部署改用外部 SQL 适配器或 Redis。
- **Redis**：配置独立业务 key 前缀、持久化与 HA。`WithTTL` 是 run / 事件留存策略，TTL 到期后无法继续审批或回放。
- `RunCoordinator` 的 lease 只保证同一 run 的排他恢复，不替代工具自身的幂等性。工具必须接受网络中断、lease 失效或
  worker 崩溃后重新触发。
- 事件日志是回放记录，不是不可篡改审计账本；强审计、跨地域和长期保留应实现自己的 `CheckpointStore`。

```bash
cd contrib/llmcheckpoint && go test ./...
```
