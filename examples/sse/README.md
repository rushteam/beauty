# sse —— SSE 服务端推送

演示 `pkg/transport/sse` 的单连接定时推送与多连接广播，适合单向实时通知（比 WebSocket 更轻）。

## 运行

```bash
go run ./examples/sse
```

```bash
# 每秒收到 tick 事件
curl -N http://localhost:8080/time

# 每 2 秒收到 news 事件
curl -N http://localhost:8080/news
```

## 说明

- `/time`：`sse.Handler` 在连接存活期间按 ticker 推送 `Event{Event, Data}`。
- `/news`：`sse.Broadcast` 把 `stream.Broadcaster[sse.Event]` fan-out 给所有 `/news` 客户端。
- 客户端断开时 `r.Context().Done()` 结束 handler，无需手动清理。
