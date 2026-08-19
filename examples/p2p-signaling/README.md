# p2p-signaling —— 浏览器 P2P DataChannel 通信

多个浏览器标签页通过信令服务器自动发现彼此、建立 **WebRTC DataChannel 直连**——
之后的数据传输完全在 peers 之间流动,不经服务器。

```
浏览器A ─┐                                    ┌─ 直连 DataChannel(P2P)
浏览器B ─┼── WS 信令(offer/answer/ICE) ──→ ─┤  · reliable 通道:聊天消息
浏览器C ─┘       pkg/transport/p2p/signaling             └─ · unreliable 通道:位置同步
```

核心包:
- [`pkg/transport/p2p`](../../pkg/transport/p2p) — P2P 双通道抽象(`PeerConn` 接口)
- [`pkg/transport/p2p/signaling`](../../pkg/transport/p2p/signaling) — WebSocket 信令服务
- [`pkg/transport/p2p/topology`](../../pkg/transport/p2p/topology) — 拓扑策略(FullMesh/Star/MatchPairs)

## 与 webrtc-voice-room 的区别

| | p2p-signaling (本示例) | webrtc-voice-room |
|---|---|---|
| 架构 | **P2P mesh**:数据 peer↔peer | **SFU**:媒体 peer↔server |
| 数据面 | DataChannel(任意二进制/文本) | RTP(音视频轨道) |
| 服务器角色 | 仅信令配对(建连后可断开) | 持续转发 RTP 包 |
| 适用场景 | 游戏状态同步、协作编辑、文件传输 | 多人语音/视频通话 |

## 运行

```bash
go run ./examples/p2p-signaling
# 多个浏览器/标签页打开 http://localhost:8080/
# 输入相同房间名 → 加入 → peers 自动发现并建连
# 在输入框发送消息(走 reliable DataChannel)
```

> 局域网/本机可直接通;跨 NAT 需要配 STUN/TURN(前端代码中的 ICE servers)。

## 工作流程

1. 客户端 WebSocket 连接到 `/ws`,发送 `join` 消息(带房间名)
2. 信令服务器根据 **拓扑策略** 决定连接对,向 initiator 发送 `peer_joined(initiator=true)`
3. Initiator 创建 `RTCPeerConnection` + DataChannel,生成 offer 经信令中转
4. Responder 收到 offer,创建 answer 回传;双方交换 ICE candidate
5. DataChannel 建立——之后数据直连,信令服务器不再参与

## 拓扑策略

默认使用 `topology.FullMesh{}`(所有 peer 互连)。替换一行即可切换:

```go
// 星型:所有人只与 host 连接
sig := signaling.NewServer(
    signaling.WithTopology(topology.Star{Hub: "host-peer-id"}),
)

// 1v1 配对(使用 Queue 在信令层手动管理)
queue := topology.NewQueue()
```

## 信令协议

客户端 → 服务端:

| Event | Data | 说明 |
|---|---|---|
| `join` | `{room, peer_id?}` | 加入房间(peer_id 可选) |
| `relay` | `{to, data}` | 中转信令给目标 peer |
| `leave` | — | 离开房间 |

服务端 → 客户端:

| Event | Data | 说明 |
|---|---|---|
| `assign_id` | `{peer_id}` | 分配的 peer ID |
| `peer_joined` | `{peer_id, initiator}` | 新 peer 加入;initiator=true 表示你应发起 offer |
| `peer_left` | `{peer_id}` | peer 离开 |
| `signal` | `{from, data}` | 来自其他 peer 的中转信令 |

## 机制 vs 策略

**机制(pkg/transport/p2p/signaling)**:信令中继、peer 发现、拓扑驱动的连接编排。

**策略(由你决定)**:
- **鉴权**:join 前校验 token(中间件或在 Handler 外层包一层)
- **STUN/TURN**:前端 `RTCPeerConnection` 的 `iceServers` 配置
- **房间上限**:在 `join` 逻辑中检查 `PeerCount` 拒绝
- **DataChannel 协议**:本 demo 用纯文本;游戏场景用 protobuf/flatbuffers
- **可靠 vs 不可靠**:创建多个 DataChannel 分别设置 `ordered` / `maxRetransmits`
