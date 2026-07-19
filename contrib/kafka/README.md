# contrib/kafka —— pkg/mq 的 Kafka broker 绑定(独立模块)

实现 `pkg/mq` 的 `Publisher`/`Subscriber`,基于 segmentio/kafka-go。核心出接口、本模块出实现。

```bash
go get github.com/rushteam/beauty/contrib/kafka@latest
```

## 用法

```go
import (
    bkafka "github.com/rushteam/beauty/contrib/kafka"
    "github.com/rushteam/beauty/pkg/mq"
)

pub := bkafka.NewPublisher([]string{"127.0.0.1:9092"})
defer pub.Close()
pub.Publish(ctx, mq.Message{Topic: "orders", Key: "user-1", Body: data})

sub := bkafka.NewSubscriber([]string{"127.0.0.1:9092"})
consumer := mq.NewConsumer(sub).
    Handle("orders", handle, mq.WithGroup("order-workers")) // 必须指定 group
beauty.New(beauty.WithService(consumer))
```

## 语义

- topic → Kafka topic;`mq.Message.Key` → Kafka Key(决定分区、保序);`Headers` → Kafka Headers。
- `mq.WithGroup(g)` → Kafka **consumer group**。Kafka 消费天生基于 group,故 `Subscribe`
  **必须**带 group(否则返回 `ErrGroupRequired`);要"扇出"给每个实例配**不同** group。
- 投递:**at-least-once**——handler 成功后才提交 offset;失败不提交、下次重投。故 handler 应
  **幂等**(可配 `pkg/idempotency` 或 `mq.Retry`)。订阅随 ctx 取消而停。

> 端到端需真实 Kafka broker;单测覆盖消息映射与 group 前置校验,broker 互操作请在有 Kafka 的环境验证。
