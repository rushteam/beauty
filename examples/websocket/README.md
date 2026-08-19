# websocket —— WebSocket 示例

演示 `pkg/transport/ws` 的 Echo 连接与 JSON 广播两种常见模式，配合 `pkg/messaging/stream` 做 fan-out。

## 运行

```bash
go run ./examples/websocket
```

```bash
# Echo：发什么回什么
websocat ws://localhost:8080/echo

# 广播：每 2 秒收到一条 JSON 通知
websocat ws://localhost:8080/notice
```

## 说明

- `/echo`：`ws.Handler` 读一条写一条，适合调试连通性。
- `/notice`：`ws.BroadcastJSON(hub)` 把 `stream.Broadcaster[notice]` 推给所有订阅连接；后台 goroutine 定时 `Publish`。
- 慢客户端缓冲区满时丢帧，不阻塞广播源（`stream.WithBufferSize` 可调）。
