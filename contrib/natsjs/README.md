# contrib/natsjs —— pkg/mq 的 NATS JetStream 绑定(持久化,独立模块)

实现 `pkg/mq` 的 `Publisher`/`Subscriber`,用 **JetStream** 提供**持久化 + at-least-once**:
消息落盘、消费确认、失败重投、断线可续。是 [`contrib/nats`](../nats)(core NATS,at-most-once)
的"可靠"版。

```bash
go get github.com/rushteam/beauty/contrib/natsjs@latest
```

## 用法

```go
import (
    bjs "github.com/rushteam/beauty/contrib/natsjs"
    "github.com/rushteam/beauty/pkg/mq"
)

conn, _ := bjs.Connect("nats://127.0.0.1:4222")
defer conn.Close()

// 先建 Stream 覆盖 subject(JetStream 必需)
conn.EnsureStream(ctx, "ORDERS", "orders")

// 发布(持久化)
conn.Publish(ctx, mq.Message{Topic: "orders", Body: data})

// 消费:durable group 竞争消费,handler 成功 Ack、失败 Nak 重投
consumer := mq.NewConsumer(conn).
    Handle("orders", handle, mq.WithGroup("order-workers"))
beauty.New(beauty.WithService(consumer))
```

## 语义

- topic → JetStream subject(须先 `EnsureStream` 建 Stream 覆盖它)。
- `mq.WithGroup(g)` → **durable consumer**(同名 durable 竞争消费、断线续消费);不设组 →
  **ephemeral consumer**(每订阅者各一份 → 扇出)。
- `Headers` → 消息头;`Key` 走 `X-MQ-Key` 头透传。
- 投递:**at-least-once**,AckExplicit——handler 成功 `Ack`、失败 `Nak`(立即重投)。故 handler 应
  **幂等**(可配 `pkg/idempotency`)。订阅随 ctx 取消停止。

## 何时选它 vs contrib/nats

| | contrib/nats(core) | contrib/natsjs(JetStream) |
|---|---|---|
| 投递 | at-most-once(丢了就丢) | at-least-once(落盘、重投、可续) |
| 开销 | 最低 | 需 Stream、落盘 |
| 适合 | 实时通知、可丢的状态推送 | 订单/任务/事件等不可丢的业务消息 |

单测用内嵌开启 JetStream 的 `nats-server` 做真实往返:先发后订的**持久化**、Nak **重投**。
