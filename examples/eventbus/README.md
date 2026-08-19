# eventbus —— 事件总线

演示进程内 `pkg/messaging/eventbus` 按 topic 分发事件，多模块独立订阅、互不影响。

## 运行

```bash
go run ./examples/eventbus
```

## 说明

- `Subscribe(topic, handler)` 注册监听；同一 topic 可有多个订阅者（通知、审计、成就各收一份）。
- `Publish(topic, event)` 同步 fan-out，返回通知的订阅者数量。
- 返回的 `Unsubscribe` 函数可退订；退订后不再收到该 topic 事件。
- 与 `pkg/messaging/mq` 的区别：eventbus 是进程内；跨进程异步通信用 mq。
