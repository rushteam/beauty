# contrib/rabbitmq — RabbitMQ MQ 绑定

实现 `pkg/messaging/mq.Publisher` / `Subscriber`(topic exchange,confirm 模式,at-least-once)。

```go
pub, err := rabbitmq.NewPublisher("amqp://guest:guest@localhost:5672/")
sub, err := rabbitmq.NewSubscriber("amqp://guest:guest@localhost:5672/")
```

Publisher 在连接/channel 断开时会自动重连并重试一次 Publish。

```bash
cd contrib/rabbitmq && go test ./...
# 端到端(需本地 RabbitMQ):
RABBITMQ_URL=amqp://guest:guest@localhost:5672/ go test -run Integration ./...
```
