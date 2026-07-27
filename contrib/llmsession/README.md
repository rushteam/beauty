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
