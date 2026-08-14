# statesync-delta — AOI 增量同步 + 延迟补偿演示

在 [`statesync`](../statesync) 基础上:

- 用 `pkg/replicate.Projector` 下发 **Delta**(spawn/update/despawn/baseline),替代每帧全量 `Visible`
- 用 `pkg/inputclock` + `pkg/lagcomp` 演示 `WorldAt` 补偿查询
- `gameloop.PushInput` 携带 `client_frame`

```bash
go run ./examples/statesync-delta
```

端口 `127.0.0.1:8125`;payload 为 `ServerMsg{kind:delta|catchup}`。

客户端收到 baseline 后发送 `{"kind":"ack","ack":{"last_ack_frame":N}}`;服务器经 `ViewerTrack.OnAck` 补发 CatchUp。
