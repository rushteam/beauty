# contrib/kafka —— pkg/messaging/mq 的 Kafka broker 绑定(独立模块)

实现 `pkg/messaging/mq` 的 `Publisher`/`Subscriber`,基于 [twmb/franz-go](https://github.com/twmb/franz-go),
默认挂载官方 OTel 插件 [kotel](https://github.com/twmb/franz-go/tree/master/plugin/kotel)
(publish / receive / process span + broker metrics)。

```bash
go get github.com/rushteam/beauty/contrib/kafka@latest
```

## 用法

```go
import (
    bkafka "github.com/rushteam/beauty/contrib/kafka"
    "github.com/rushteam/beauty/pkg/messaging/mq"
)

pub, err := bkafka.NewPublisher([]string{"127.0.0.1:9092"})
if err != nil { /* ... */ }
defer pub.Close()
pub.Publish(ctx, mq.Message{Topic: "orders", Key: "user-1", Body: data})

sub := bkafka.NewSubscriber([]string{"127.0.0.1:9092"})
consumer := mq.NewConsumer(sub).
    Handle("orders", handle, mq.WithGroup("order-workers")) // 必须指定 group
beauty.New(beauty.WithService(consumer))
```

SASL / TLS / ClientID 等通过透传 franz-go Opt:

```go
import "github.com/twmb/franz-go/pkg/kgo"

pub, _ := bkafka.NewPublisher(brokers,
    bkafka.WithClientOpts(
        kgo.ClientID("order-svc"),
        // kgo.SASL(...), kgo.DialTLS(...),
    ),
)
```

关闭内置 OTel(默认开启,未配 TracerProvider 时为 noop):

```go
pub, _ := bkafka.NewPublisher(brokers, bkafka.WithoutOTel())
sub := bkafka.NewSubscriber(brokers, bkafka.WithoutSubscriberOTel())
```

> Kafka 场景用本模块内置 kotel 即可,不必再套 `pkg/messaging/mq/otelmq`(避免双重 Inject)。
> `otelmq` 仍适用于 InProc / NATS 等非 franz 传输。

## 语义

- topic → Kafka topic;`mq.Message.Key` → Kafka Key(默认 StickyKeyPartitioner,同 Key 保序);`Headers` → Kafka Headers。
- `mq.WithGroup(g)` → Kafka **consumer group**。`Subscribe` **必须**带 group(否则
  `ErrGroupRequired`);要"扇出"给每个实例配**不同** group。
- 投递:**at-least-once**——`DisableAutoCommit` + handler 成功后 `CommitRecords`;失败不提交。
  故 handler 应**幂等**。订阅随 ctx 取消而停。
- OTel:kotel 自动创建 publish/receive span,消费循环内 `WithProcessSpan` 覆盖业务处理。

> 端到端需真实 Kafka broker;单测覆盖消息映射与 group 前置校验,broker 互操作请在有 Kafka 的环境验证。
