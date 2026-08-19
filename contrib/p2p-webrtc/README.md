# contrib/p2p-webrtc —— WebRTC P2P 传输(独立模块)

实现 beauty `pkg/transport/p2p.Transport` 接口,基于 [pion/webrtc](https://github.com/pion/webrtc)
DataChannel 提供可靠/不可靠双通道。适合浏览器 ↔ Go 直连、复杂 NAT 穿透、与前端
`RTCPeerConnection` 对接的游戏/协作场景。

```bash
go get github.com/rushteam/beauty/contrib/p2p-webrtc@latest
```

## 用法

```go
import webrtctransport "github.com/rushteam/beauty/contrib/p2p-webrtc"

// signalFunc 由 pkg/transport/p2p/signaling 客户端注入,负责在两个 peer 间传递 SDP/ICE
transport := webrtctransport.New(localPeerID, signalFunc,
    webrtctransport.WithICEServers([]webrtc.ICEServer{
        {URLs: []string{"stun:stun.l.google.com:19302"}},
        {URLs: []string{"turn:turn.example.com:3478"}, Username: "u", Credential: "p"},
    }),
)

// 主动连接
conn, err := transport.Connect(ctx, remotePeerID)
conn.SendReliable([]byte("hello"))
msg := <-conn.Recv()

// 被动接受(对端 offer 经 HandleSignal 处理后)
conn, err = transport.Accept(ctx)

// 信令回调:收到远端 signal 后
transport.HandleSignal(ctx, fromPeer, webrtctransport.SignalMessage{
    Type: "offer", SDP: "...",
})
```

默认创建 `reliable`(有序)与 `unreliable`(无序、无重传)两个 DataChannel,与浏览器端约定一致。
WebRTC 不监听固定地址,`Addr()` 返回空串;连接建立依赖外部信令服务。

## 与其他 Transport 对比

| | TCP | QUIC | WebRTC |
|---|---|---|---|
| NAT 穿透 | 否 | 需自行打洞 | ICE 内建(STUN/TURN) |
| 浏览器 | 否 | 否 | 是 |
| 依赖 | 无 | quic-go | pion/webrtc + 信令 |

## 边界

信令协议、TURN 部署、peer 身份校验都是 policy;本包只做 Transport 实现。依赖 beauty core
(`pkg/transport/p2p` 接口)。
