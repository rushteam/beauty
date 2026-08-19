# statesync-quic — QUIC 双通道增量同步 + CatchUp

与 [`statesync`](../statesync) 相同的世界/AOI/replicate 逻辑,传输改为 QUIC 双通道:

| 通道 | 内容 |
|------|------|
| 可靠流 | Hello、Cmd、Ack、CatchUp、resync |
| 不可靠数据报 | `replicate.Delta` |

```bash
go run ./examples/statesync-quic
```

端口 `127.0.0.1:8443`。Ack/CatchUp 语义见 `pkg/game/replicate/catchup.go`。
