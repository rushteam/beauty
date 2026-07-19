# contrib/nats —— pkg/mq 的 NATS broker 绑定(独立模块)

实现 `pkg/mq` 的 `Publisher`/`Subscriber`,用 NATS 承载跨服务异步。核心出接口、本模块出实现。

```bash
go get github.com/rushteam/beauty/contrib/nats@latest
```

## 用法

```go
import (
    bnats "github.com/rushteam/beauty/contrib/nats"
    "github.com/rushteam/beauty/pkg/mq"
)

conn, _ := bnats.Connect("nats://127.0.0.1:4222")
defer conn.Close()

// conn 同时是 mq.Publisher 与 mq.Subscriber
consumer := mq.NewConsumer(conn).
    Handle("orders", handle, mq.WithGroup("order-workers")) // queue group 竞争消费
app := beauty.New(beauty.WithService(consumer))

conn.Publish(ctx, mq.Message{Topic: "orders", Body: data})
```

## 语义

- topic → NATS subject;`mq.WithGroup(g)` → NATS **queue group**(同组竞争消费);不设组 → 扇出。
- `Headers` → NATS 头;`Key` 走 `X-MQ-Key` 头透传。
- 投递:NATS core 为 **at-most-once**(不持久、不重投),与 mq 进程内实现一致。要持久化/重投用
  JetStream(可另起 `contrib/natsjs`)。订阅随 `Subscribe` 的 ctx 取消而退订。

单测用内嵌 `nats-server` 做真实往返(扇出 / 队列组 / 退订)。
