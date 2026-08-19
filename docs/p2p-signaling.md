# P2P DataChannel 信令与组网

beauty 的 P2P 能力把 WebRTC 从"媒体通话工具"变为**通用 P2P 数据管道**——
peers 之间建立 DataChannel 直连,数据不经服务器,延迟更低、带宽成本更低。

在 beauty 已有的 WebRTC(pion)、WebSocket(pkg/transport/ws)、QUIC(pkg/transport/quic)、
presence(pkg/transport/presence)基础上,补齐了**信令编排 + peer 发现 + 拓扑策略 + 双通道抽象**
这一层。

## 包结构

```
pkg/transport/p2p/
├── p2p.go           # PeerConn / Message / Network / Transport 核心接口
├── network.go       # LocalNetwork: 内存实现,管理多条 PeerConn
├── topology/
│   └── topology.go  # Topology 接口 + FullMesh / Star / MatchPairs / Queue
└── signaling/
    └── signaling.go # WebSocket 信令服务器(房间、peer 发现、信令中转)
```

## 架构全景

```
┌─────────────────────────────────────────────────────────────────┐
│                          业务层                                   │
│  (游戏状态同步 / 协作编辑 / 文件传输 / P2P 聊天)                │
├────────────────────────────────┬────────────────────────────────┤
│         PeerConn 接口           │        Network 接口             │
│  SendReliable / SendUnreliable │  Peers / Broadcast / Events    │
├────────────────────────────────┴────────────────────────────────┤
│                       传输层(可插拔)                             │
│  ┌──────────────────┐  ┌──────────────────┐                    │
│  │ WebRTC DataChannel│  │  QUIC(pkg/transport/quic) │                    │
│  │  (浏览器可达)     │  │  (原生/服务端)   │                    │
│  └──────────────────┘  └──────────────────┘                    │
├─────────────────────────────────────────────────────────────────┤
│                     信令 & 拓扑层                                 │
│  pkg/transport/p2p/signaling      pkg/transport/p2p/topology                        │
│  (WS 信令中继)          (FullMesh / Star / MatchPairs)          │
└─────────────────────────────────────────────────────────────────┘
```

## 核心接口

### PeerConn — 双通道 peer 连接

```go
type PeerConn interface {
    ID() PeerID
    SendReliable(data []byte) error    // 可靠有序(类 TCP)
    SendUnreliable(data []byte) error  // 不可靠无序(类 UDP)
    Recv() <-chan Message
    Context() context.Context
    Close() error
}
```

为什么要双通道:
- **可靠通道**:聊天消息、RPC、游戏事件——不能丢、保序
- **不可靠通道**:位置同步、输入帧、状态快照——丢了无所谓,要最新值

### Network — P2P 网络视图

```go
type Network interface {
    LocalID() PeerID
    Peers() []PeerConn
    Peer(id PeerID) PeerConn
    Events() <-chan PeerEvent      // 连接/断开事件
    Broadcast(data []byte, reliable bool) error
    Close() error
}
```

### Topology — 拓扑策略

```go
type Topology interface {
    OnPeerJoin(newPeer PeerID, existingPeers []PeerID) []PeerPair
    OnPeerLeave(leavingPeer PeerID, remainingPeers []PeerID) []PeerPair
}
```

内置策略:

| 策略 | 连接数 | 适用场景 |
|---|---|---|
| `FullMesh` | n*(n-1)/2 | ≤8 人小规模(游戏对战、协作) |
| `Star{Hub}` | n-1 | 主从模式(房主 host) |
| `MatchPairs` / `Queue` | n/2 | 1v1 对战匹配 |

## 信令协议

信令走 WebSocket,JSON 信封格式:

```json
{"event": "join", "data": {"room": "game-1", "peer_id": "alice"}}
{"event": "relay", "data": {"to": "bob", "data": "<offer/answer/candidate>"}}
```

完整流程:

```
Client A                    Server                    Client B
   │─── join(room) ──────────→│                          │
   │←── assign_id(A) ─────────│                          │
   │                           │←── join(room) ──────────│
   │                           │──→ assign_id(B) ────────→│
   │←── peer_joined(B,init) ──│──→ peer_joined(A,resp) ─→│
   │                           │                          │
   │─── relay(to:B, offer) ──→│──→ signal(from:A) ──────→│
   │←── signal(from:B) ───────│←── relay(to:A, answer) ──│
   │─── relay(to:B, cand) ───→│──→ signal(from:A) ──────→│
   │←── signal(from:B) ───────│←── relay(to:A, cand) ────│
   │                           │                          │
   │═══════════ DataChannel 直连(不经服务器)═════════════│
```

## 与现有包的关系

| 包 | 角色 |
|---|---|
| `pkg/transport/ws` | 承载信令的 WebSocket 连接 |
| `pkg/transport/presence` | 可选——跨节点 peer 发现(分布式部署时) |
| `pkg/media/webrtc` | 共享 pion 依赖;P2P 用 DataChannel 而非 media track |
| `pkg/media/webrtc/sfu` | 对比:SFU 是 peer↔server;P2P 是 peer↔peer |
| `pkg/transport/quic` | 可选——原生客户端间的可靠/不可靠双通道替代方案 |
| `pkg/game/gameloop` | 可选——挂在 PeerConn 上做固定帧率游戏同步 |

## 使用方式

### 服务端(3 行)

```go
sig := signaling.NewServer(
    signaling.WithTopology(topology.FullMesh{}),
    signaling.WithWSOptions(ws.WithOriginPatterns("*")),
)
mux.Handle("/ws", sig.Handler())
```

### 客户端(浏览器 JS)

1. WebSocket 连到 `/ws`,发 `join`
2. 收到 `peer_joined(initiator=true)` → 创建 `RTCPeerConnection` + DataChannel,发 offer
3. 收到 `signal(offer)` → 创建 answer
4. 连接建立后通过 DataChannel 收发数据

完整前端代码见 [examples/p2p-signaling](../examples/p2p-signaling)。

## 扩展方向

### 小规模 P2P + 大规模降级 SFU

beauty 同时拥有 P2P 和 SFU,可以做**自动降级**:

```
≤4 人 → FullMesh P2P(延迟最低、零服务器带宽)
5~50 人 → SFU 转发(pkg/media/webrtc/sfu)
50+ 人 → 级联 SFU / CDN 分发(pkg/media/hls)
```

### 分布式信令

单节点 `signaling.Server` 可通过 `pkg/transport/presence` + `pkg/transport/router` 扩展为多节点:
- presence 追踪"peer 在哪个信令节点"
- router 跨节点转发 relay 消息
- 每个节点仍用本地 `signaling.Server` 处理本节点的 peer

### QUIC 传输后端

原生客户端(非浏览器)可跳过 WebRTC,直接用 `pkg/transport/quic`:
- Stream = 可靠通道
- Datagram = 不可靠通道
- 实现 `p2p.PeerConn` 接口即可无缝对接上层
