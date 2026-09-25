# Batching (pkg/messaging/mq — Batch)

> 中文版: [mq-batch.md](mq-batch.md)

Accumulates multiple messages into a batch and processes them together, reducing the number of I/O operations. Inspired by the Batches feature of BullMQ Pro.

## Two APIs

| API | Form | Use case |
|-----|------|------|
| `Batch(size, timeout, fn)` | Handler middleware | Simple scenarios; drop-in replacement for a handler |
| `BatchCollector` | Standalone component + `Start` lifecycle | When you need precise control over flush timing (e.g. graceful shutdown) |

## Batch — Handler Middleware

Calls `fn` once `size` messages have accumulated or after `timeout` elapses:

```go
consumer := mq.NewConsumer(broker)
consumer.Handle("orders", mq.Batch(100, time.Second, func(ctx context.Context, msgs []mq.Message) error {
    return db.BulkInsert(ctx, msgs) // write 100 rows at once
}))
```

### Semantics

- When size is reached: flushes synchronously (returns within the handler call of the current message)
- Timeout flush: triggered by a background timer, using `context.Background()`
- If fn returns an error: the whole batch is treated as failed (can be combined with an outer `mq.Retry` for retries)

## BatchCollector — Standalone Component

Finer-grained control with its own lifecycle:

```go
bc := mq.NewBatchCollector(50, 2*time.Second, func(ctx context.Context, msgs []mq.Message) error {
    return elasticBulk(ctx, msgs)
})

// register with the consumer
consumer.Handle("logs", bc.Handler())

// start periodic flushing (follows the beauty.Service pattern)
go bc.Start(ctx) // flushes the remainder and exits when ctx is canceled

// query buffer state
pending := bc.Pending()
```

### Differences from Batch

- `Batch` performs timeout flushes inside `time.AfterFunc`, so it cannot guarantee a flush during graceful shutdown
- `BatchCollector.Start` watches for ctx cancellation and **always flushes the remaining messages** on shutdown
- `BatchCollector` lets you monitor buffer backlog via `Pending()`

## Typical Scenarios

- **Bulk database writes**: MySQL/PG `INSERT ... VALUES (...), (...), ...`
- **Elasticsearch bulk**: accumulate 50 docs → one `_bulk` API call
- **Batch notifications**: email/SMS batch APIs
- **Log aggregation**: accumulate a batch, then write to Kafka / a file

## Combining with Retry

```go
h := mq.Chain(
    mq.Batch(100, time.Second, bulkWrite),
    mq.Retry(3, 500*time.Millisecond), // retry when the bulk write fails
    mq.Recover(),                       // panic protection
)
consumer.Handle("events", h)
```
