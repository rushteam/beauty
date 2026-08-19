# router —— HTTP 消息路由

演示 `pkg/router` 按 presence ID 定点投递、按 stream 群发，以及 `QueueDeferred` 攒批 flush。

## 运行

```bash
go run ./examples/router
```

```bash
curl "http://localhost:8284/join?session=s1&user=alice&channel=room1"
curl "http://localhost:8284/say?channel=room1&msg=hello"
curl "http://localhost:8284/send?session=s1&msg=hi"
curl http://localhost:8284/batch
```

## 说明

- 配合 `pkg/presence` 追踪「谁在哪个频道」；`sinkRegistry` 模拟 WebSocket 的本地投递函数。
- `SendToStream` 群发给频道所有成员；`SendToPresenceIDs` 按 session 定点（可带 `Node` 跨节点转发）。
- `QueueDeferred` + `FlushDeferred` 把多条消息合并一次投递，降低 IO 开销。
