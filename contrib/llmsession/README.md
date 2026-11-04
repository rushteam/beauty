# contrib/llmsession —— session.Store 的 SQLite / Redis 实现

为 [`llm/agent/session`](../llm/agent/session) 提供生产向持久化,不拖重 `contrib/llm` 的零依赖核心。

```bash
go get github.com/rushteam/beauty/contrib/llmsession@latest
```

## SQLite

```go
store, _ := llmsession.NewSQLite("./data/sessions.db")
defer store.Close()
mgr := &session.Manager{Store: store}
```

## Redis

```go
rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})
store, _ := llmsession.NewRedis(rdb,
    llmsession.WithKeyPrefix("myapp:sess:"),
    llmsession.WithTTL(7*24*time.Hour),
)
mgr := &session.Manager{Store: store}
```

实现同一 `session.Store` 接口,与 `MemoryStore` / `FileStore` 可互换。

## 生产建议

- **SQLite** 适合单实例或单个持久卷；不适合多个 pod 共享本地文件。
- **Redis** 适合低延迟和 TTL 会话。应设置隔离的 key 前缀、持久化策略和 HA；TTL 到期即代表会话不可恢复。
- Store 只保证一次 `Save` 的原子替换。同一会话允许并行 `Run` 时，应用需要按 session ID 串行化，或实现带版本/CAS 的 Store，
  否则最后保存的结果会覆盖较早的并发写入。
