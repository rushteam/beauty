# mq —— 消息队列抽象 + 消费者即 Service

传输无关的发布/订阅抽象([`pkg/mq`](../../pkg/mq)),补齐框架跨服务异步的空白(此前只有
进程内 `eventbus` 扇出 + `webhook` HTTP 推)。本示例用**零依赖的进程内 broker** 跑通,换真
broker(NATS/Kafka)只需替换 broker 实现,业务代码不动。

```
Publish("orders") ─▶ InProc broker ─▶ 队列组 order-workers(组内竞争消费)
                                          └─ Consumer(beauty.Service)→ handler(Recover+Retry)
```

## 运行

```bash
go run ./examples/mq
```

会看到消费者依次处理 5 条订单(带分区键与 header)。Ctrl-C 优雅停机。

## 关键点

- **消费者即 Service**:`mq.NewConsumer(broker).Handle(topic, h, opts...)` 返回的对象实现
  `beauty.Service`(Start/String/Ready),`beauty.WithService(consumer)` 挂上即随 app 停机。
- **队列组 vs 扇出**:`WithGroup("g")` → 同组**竞争消费**(多副本水平扩展,每条只投一个);
  不设 group → **扇出**(每个订阅者都收到)。语义对齐 NATS queue group / Kafka consumer group。
- **处理中间件**:`mq.Chain(h, mq.Recover(), mq.Retry(3, delay))`——`Recover` 吞 panic 不打崩
  投递,`Retry` 兜下游瞬时抖动。
- **换 broker**:`mq.NewInProc()` → 实现 `mq.Publisher`/`mq.Subscriber` 的绑定(opt-in 子包)。
  接口按 ctx 绑定订阅生命周期,同时适配 NATS(push)与 Kafka(pull)语义。

## 边界(机制 vs 策略)

- 序列化(`Body` 是 `[]byte`)、trace 透传(用 `Headers` 配 `pkg/metadata`)、分区键、broker 选型都是 policy。
- 投递保证由 broker 决定:进程内实现是 at-most-once(handler 出错不重投);要持久化/重投/
  exactly-once 用支持的 broker(如 NATS JetStream)。可靠"改库+发消息"需 Outbox(依赖持久层,暂未做)。
