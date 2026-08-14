# statesync-quic-delta — QUIC 增量同步 + CatchUp

在 [`statesync-quic`](../statesync-quic) 传输模型上叠加 [`pkg/replicate`](../../pkg/replicate):

| 通道 | 内容 |
|------|------|
| 可靠流 | Hello、Cmd、Ack、CatchUp 补发 |
| 不可靠数据报 | `replicate.Delta` |

```bash
go run ./examples/statesync-quic-delta
```

端口 `127.0.0.1:8444`。Ack/CatchUp 语义见 `pkg/replicate/catchup.go`。
