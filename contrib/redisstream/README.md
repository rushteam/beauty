# contrib/redisstream — Redis Streams MQ 绑定

实现 `pkg/messaging/mq.Publisher` / `Subscriber`(XADD / XREADGROUP,at-least-once)。

```go
pub := redisstream.NewPublisher(rdb)
sub := redisstream.NewSubscriber(rdb)
```

Redis 客户端(go-redis)自带连接重连;Subscriber 在 handler 失败时不 XACK,消息留在 pending。

```bash
cd contrib/redisstream && go test ./...
```

集成测试使用内嵌 miniredis,无需外部 Redis。
