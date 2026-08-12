# P2P Transport 实现

`pkg/p2p` 定义了传输无关的 P2P 接口(`Transport`、`PeerConn`、`Network`),
以下三种实现覆盖不同场景:

## 选型指南

| Transport | 适合场景 | 可靠通道 | 不可靠通道 | NAT 穿透 | 外部依赖 |
|-----------|---------|---------|-----------|---------|---------|
| **TCP** | 内网/局域网、服务间通信 | TCP ✅ | TCP 降级(仍可靠) | ❌ 不支持 | 无(stdlib) |
| **QUIC** | 跨机房/边缘节点、游戏服务器 | QUIC Stream ✅ | QUIC Datagram ✅ | 部分(UDP) | quic-go |
| **WebRTC** | 浏览器互联、跨 NAT 穿透 | DataChannel(ordered) ✅ | DataChannel(unordered) ✅ | ✅ ICE/STUN/TURN | pion/webrtc |

## 1. TCP Transport (`pkg/p2p/tcptransport`)

**适合场景:**
- Go 服务之间的内网/同机房 P2P 通信(无 NAT)
- 局域网联机(游戏 LAN party、IoT 设备互联)
- 需要穿透企业防火墙(TCP 443 不被拦截)
- 对可靠性要求高、对延迟不敏感

**特点:**
- 零外部依赖,只用 `net` 标准库
- `SendUnreliable` 降级为 TCP 发送(仍可靠有序)
- 双方必须网络可达(不穿 NAT)

```go
import "github.com/rushteam/beauty/pkg/p2p/tcptransport"

// 创建 transport
t, _ := tcptransport.New("node-1", ":9000",
    tcptransport.WithAddressBook(map[string]string{
        "node-2": "192.168.1.20:9000",
    }),
)
defer t.Close()

// 连接
conn, _ := t.Connect(ctx, "node-2")
conn.SendReliable([]byte("hello"))

// 或接受入站连接
conn, _ = t.Accept(ctx)
```

## 2. QUIC Transport (`pkg/p2p/quictransport`)

**适合场景:**
- 服务端之间的高性能 P2P 通信(跨数据中心、边缘节点)
- 游戏服务器之间的状态同步(真正的双通道)
- 需要 0-RTT 快速重连
- 移动端原生应用(连接迁移:WiFi↔4G 无缝切换)

**特点:**
- 可靠通道:QUIC Stream(多路复用,无跨流队头阻塞)
- 不可靠通道:QUIC Datagram(RFC 9221,真 UDP 语义——不重传)
- 内建 TLS 1.3(加密强制)
- 比 TCP 更好的 NAT 穿越能力(UDP 打洞比 TCP 容易)

```go
import (
    "github.com/rushteam/beauty/pkg/p2p/quictransport"
    "github.com/rushteam/beauty/pkg/quic"
)

t, _ := quictransport.New("node-1", ":8443",
    []quic.Option{quic.WithTLSConfig(tlsConf)},
    quictransport.WithAddressBook(map[string]string{
        "node-2": "10.0.1.5:8443",
    }),
    quictransport.WithDialOptions(quic.WithClientTLSConfig(clientTLS)),
)
defer t.Close()

conn, _ := t.Connect(ctx, "node-2")
conn.SendReliable([]byte("critical event"))   // QUIC Stream
conn.SendUnreliable([]byte("position update")) // QUIC Datagram(可丢)
```

## 3. WebRTC Transport (`contrib/p2p-webrtc`)

**适合场景:**
- 浏览器与 Go 服务之间的 P2P 通信(唯一方案)
- Go 进程之间需要穿越复杂 NAT(ICE 自动穿透)
- 需要与前端 JS 客户端(`p2p-client.js`)对接
- 游戏客户端(Web/Electron)与匹配服务器直连

**特点:**
- ICE 框架自动 NAT 穿透(STUN + TURN fallback)
- 与浏览器 RTCPeerConnection 完全兼容
- 需要信令服务配合(`pkg/p2p/signaling`)
- 可靠通道:ordered DataChannel
- 不可靠通道:unordered DataChannel(maxRetransmits=0)

```go
import webrtc "github.com/rushteam/beauty/contrib/p2p-webrtc"

// signalFunc 由信令客户端提供(通过 WebSocket 发给对端)
t := webrtc.New("player-1", signalFunc,
    webrtc.WithICEServers([]webrtc.ICEServer{
        {URLs: []string{"stun:stun.l.google.com:19302"}},
        {URLs: []string{"turn:my-turn.example.com"}, Username: "u", Credential: "p"},
    }),
)
defer t.Close()

// 信令客户端收到对端消息时调用:
t.HandleSignal(ctx, "player-2", signalMsg)

// 主动连接(发起 offer)
conn, _ := t.Connect(ctx, "player-2")
conn.SendReliable([]byte("game event"))
conn.SendUnreliable([]byte("input frame"))

// 或接受连接(对端发起 offer)
conn, _ = t.Accept(ctx)
```

## 架构图

```
                        ┌─────────────────┐
                        │   pkg/p2p       │ ← 接口层(Transport, PeerConn, Network)
                        │   p2p.go        │
                        └────────┬────────┘
                                 │
              ┌──────────────────┼──────────────────┐
              │                  │                  │
    ┌─────────▼─────────┐ ┌─────▼─────────┐ ┌─────▼───────────────┐
    │ pkg/p2p/          │ │ pkg/p2p/      │ │ contrib/p2p-webrtc  │
    │ tcptransport      │ │ quictransport │ │                     │
    │                   │ │               │ │ (pion/webrtc v4)    │
    │ · net.Conn        │ │ · pkg/quic    │ │ · ICE/DTLS/SCTP     │
    │ · 零依赖          │ │ · quic-go     │ │ · DataChannel       │
    └───────────────────┘ └───────────────┘ └─────────────────────┘
         内网/LAN              跨机房/边缘         浏览器/NAT 穿透
```

## 如何选择

1. **双方在同一内网/机房?** → TCP(最简单、零配置)
2. **需要真正的不可靠通道(游戏位置同步)?** → QUIC 或 WebRTC
3. **一端是浏览器?** → WebRTC(唯一选择)
4. **需要穿 NAT 但不涉及浏览器?** → WebRTC(ICE 最强)或 QUIC(如果能控制网络)
5. **需要加密?** → QUIC(内建 TLS)或 WebRTC(内建 DTLS)
6. **追求最低延迟?** → QUIC Datagram 或 WebRTC unordered DataChannel
