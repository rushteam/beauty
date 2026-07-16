# statesync-quic —— 状态同步搬到 QUIC(状态走数据报 / 指令走可靠流)

和 [`examples/statesync`](../statesync)(WebSocket 版)**同样的权威模拟 + AOI 逻辑**,
只把传输层换成 [`pkg/quic`](../../pkg/quic),并按可靠性把两类数据分到一条连接的两种通道:

| 数据 | 通道 | 为什么 |
|---|---|---|
| 关键指令(移动 `Cmd`、握手 `Hello`) | **可靠有序流** `OpenStream`/`AcceptStream` | 丢不得、要顺序;QUIC 流像 TCP 但跨流无队头阻塞 |
| 高频状态(AOI 视野内实体 `View`) | **不可靠数据报** `SendDatagram`/`ReceiveDatagram` | 丢了等下一帧、不重传不阻塞——位置更新只关心最新 |

## 连接协议

QUIC 没有 URL query,用握手识别玩家:

```
客户端 → 开一条可靠流 → 发 Hello{player}     （识别身份)
        → 之后在同一条流上持续发 Cmd          （可靠指令)
服务器 ← AcceptStream → 解 Hello → World.Join
        ← 每帧算该玩家 AOI → SendDatagram(View)  （不可靠状态)
```

## 运行

```bash
go run ./examples/statesync-quic
```

出生点同 WebSocket 版(alice↔bob 相邻、carol 远处),3 个 QUIC bot 端到端自校验:

```
──────── AOI(状态同步 / QUIC)校验 ────────
  alice  视野内: [bob]            (期望 [bob]) ✅
  bob    视野内: [alice]          (期望 [alice]) ✅
  carol  视野内: []               (期望 []) ✅
结论: ✅ 指令走可靠流、状态走数据报,AOI 端到端一致
```

## 复用到的 beauty 原语

| 原语 | 作用 |
|---|---|
| `pkg/quic` | QUIC 传输:可靠流(指令)+ 不可靠数据报(状态),`Server` 挂进 `beauty.WithService` |
| `pkg/gameloop` | 定步长 tick + 输入聚合 + 权威世界扇出 |
| `pkg/spatial` | 每帧网格索引,`Nearby` 做 AOI 视野过滤 |

> 说明:数据报受 MTU 限制(≲1200B),本示例对超大 View 直接跳过;生产中大快照应
> 分片或走可靠流。QUIC 用的是开发自签证书(客户端 `WithInsecureSkipVerify`),生产
> 传 `WithTLSConfig`;高负载前先按 `pkg/quic` 包注释放开 UDP 缓冲 sysctl。
