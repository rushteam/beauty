# presence —— 在线状态

演示 `pkg/presence` 追踪用户在线与频道成员，支持 join/leave 事件回调。

## 运行

```bash
go run ./examples/presence
```

```bash
curl "http://localhost:8283/online?user=alice&session=s1&channel=room1"
curl "http://localhost:8283/online?user=bob&session=s2&channel=room1"
curl "http://localhost:8283/members?channel=room1"
curl "http://localhost:8283/offline?session=s1&channel=room1&user=alice"
```

## 说明

- `Track(session, stream, meta)` 登记在场；`Untrack` 移除；`ListByStream` 查频道成员列表。
- 构造函数传入回调，join/leave 时打印日志（可对接 eventbus、审计等）。
- 与 `pkg/router` 配合可实现「发消息给某频道所有人」。
