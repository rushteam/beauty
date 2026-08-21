# 批处理 (pkg/messaging/mq — Batch)

将多条消息攒成一批统一处理,减少 I/O 次数。借鉴 BullMQ Pro 的 Batches 特性。

## 两种 API

| API | 形态 | 适用 |
|-----|------|------|
| `Batch(size, timeout, fn)` | Handler 中间件 | 简单场景,直接替换 handler |
| `BatchCollector` | 独立组件 + `Start` 生命周期 | 需要精确控制 flush 时机(如 graceful shutdown) |

## Batch — Handler 中间件

攒满 `size` 条或超时 `timeout` 后调用 `fn`:

```go
consumer := mq.NewConsumer(broker)
consumer.Handle("orders", mq.Batch(100, time.Second, func(ctx context.Context, msgs []mq.Message) error {
    return db.BulkInsert(ctx, msgs) // 一次写入 100 条
}))
```

### 语义

- 攒满 size 时:同步 flush(在当前消息的 handler 调用中返回)
- 超时 flush:后台 timer 触发,使用 `context.Background()`
- fn 返回 error:整批视为失败(可配合外层 `mq.Retry` 重试)

## BatchCollector — 独立组件

更精细的控制,有独立生命周期:

```go
bc := mq.NewBatchCollector(50, 2*time.Second, func(ctx context.Context, msgs []mq.Message) error {
    return elasticBulk(ctx, msgs)
})

// 注册到 consumer
consumer.Handle("logs", bc.Handler())

// 启动定时 flush(实现 beauty.Service 模式)
go bc.Start(ctx) // ctx 取消时 flush 剩余并退出

// 查询缓冲状态
pending := bc.Pending()
```

### 与 Batch 的区别

- `Batch` 超时 flush 在 `time.AfterFunc` 中执行,无法保证 graceful shutdown 时 flush
- `BatchCollector.Start` 监听 ctx 取消,停机时**一定会 flush 剩余消息**
- `BatchCollector` 可通过 `Pending()` 监控缓冲积压

## 典型场景

- **批量写库**:MySQL/PG 的 `INSERT ... VALUES (...), (...), ...`
- **Elasticsearch bulk**:攒 50 条 → 一次 `_bulk` API
- **批量发送通知**:邮件/短信批量接口
- **日志聚合**:攒一批再写 Kafka / 文件

## 配合 Retry

```go
h := mq.Chain(
    mq.Batch(100, time.Second, bulkWrite),
    mq.Retry(3, 500*time.Millisecond), // 批量写失败时重试
    mq.Recover(),                       // panic 保护
)
consumer.Handle("events", h)
```
