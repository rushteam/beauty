# statesync — WebSocket 状态同步 + replicate 增量 + CatchUp

权威模拟 + `spatial` AOI + `pkg/game/replicate` 增量下发,含 Ack/CatchUp/resync 协议。

| 方向 | 消息 |
|------|------|
| 客户端 → 服务器 | `ClientMsg{kind:cmd\|ack\|resync}` |
| 服务器 → 客户端 | `ServerMsg{kind:delta\|catchup}` |

- **delta**: `replicate.Delta`(spawn/update/despawn/baseline)
- **catchup**: 补发缺口;`truncated=true` 时客户端发 `resync` 触发 baseline 重同步
- **延迟补偿**: `inputclock` + `snapbuf` + `lagcomp`(cmd 带 `client_frame`)

```bash
go run ./examples/statesync
```

端口 `127.0.0.1:8124`;进程内 3 bot 校验 baseline、CatchUp 与 AOI。
