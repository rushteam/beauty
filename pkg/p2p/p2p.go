// Package p2p 提供面向 P2P 数据通道的传输无关抽象:把 WebRTC DataChannel 从
// "媒体通话工具"变为"通用 P2P 数据管道",支持可靠/不可靠双通道语义。
//
// 设计思路:
//   - 可靠通道:聊天消息、RPC 请求、游戏事件(不能丢、保序)
//   - 不可靠通道:位置同步、输入帧、状态快照(丢了无所谓,要最新值)
//
// 本包只定义接口与领域类型;具体传输由子包实现:
//   - WebRTC DataChannel(浏览器可达):pkg/p2p/signaling 处理信令
//   - QUIC(纯原生/服务端场景):复用 pkg/quic
//
// 边界(机制而非策略):本包不处理鉴权、房间模型、消息序列化——那些是 policy,留给上层。
package p2p

import (
	"context"
	"io"
)

// PeerID 标识一个 Peer。
type PeerID = string

// Message 是从 peer 收到的一条消息。
type Message struct {
	From     PeerID // 发送方 peer ID
	Data     []byte // 载荷
	Reliable bool   // true=通过可靠通道到达; false=通过不可靠通道到达
}

// PeerConn 是一条到远端 peer 的双通道连接(可靠 + 不可靠)。
// 实现者须保证 SendReliable/SendUnreliable 并发安全。
type PeerConn interface {
	// ID 返回远端 peer 的标识。
	ID() PeerID

	// SendReliable 通过可靠有序通道发送数据(类似 TCP 语义)。
	SendReliable(data []byte) error

	// SendUnreliable 通过不可靠无序通道发送数据(类似 UDP 语义)。
	// 超过底层 MTU 的负载可能被丢弃或返回错误——大负载走 SendReliable。
	SendUnreliable(data []byte) error

	// Recv 返回接收通道;关闭连接后通道会被关闭。
	Recv() <-chan Message

	// Context 返回连接生命周期 ctx(连接断开时取消)。
	Context() context.Context

	// Close 关闭连接。幂等。
	Close() error
}

// PeerState 表示 peer 连接状态。
type PeerState int

const (
	PeerStateNew          PeerState = iota // 已知但未连接
	PeerStateConnecting                    // 正在握手
	PeerStateConnected                     // 已连接
	PeerStateDisconnected                  // 已断开
)

// PeerEvent 是 peer 生命周期事件。
type PeerEvent struct {
	PeerID PeerID
	State  PeerState
	Conn   PeerConn // PeerStateConnected 时有效
}

// Network 是一个 P2P 网络视图:管理多条 PeerConn 的集合。
// 上层(如信令服务)负责驱动连接建立;Network 提供已建立连接的访问与事件通知。
type Network interface {
	// LocalID 返回本节点的 peer ID。
	LocalID() PeerID

	// Peers 返回当前已连接的所有 peer。
	Peers() []PeerConn

	// Peer 按 ID 获取已连接的 peer;不存在返回 nil。
	Peer(id PeerID) PeerConn

	// Events 返回 peer 生命周期事件通道。
	Events() <-chan PeerEvent

	// Broadcast 向所有已连接的 peer 广播。reliable 决定走哪条通道。
	// 返回第一个遇到的错误(或 nil);部分 peer 发送失败不影响其余。
	Broadcast(data []byte, reliable bool) error

	// Close 关闭网络(断开所有 peer 连接)。
	Close() error
}

// Transport 是对底层传输能力的抽象,用于可插拔实现(WebRTC / QUIC / TCP)。
// 每个 Transport 既能主动连接远端 peer,也能接受入站连接。
type Transport interface {
	io.Closer

	// Connect 主动向目标 peer 发起连接。地址解析由具体实现负责(如通过构造时传入的地址簿)。
	Connect(ctx context.Context, peerID PeerID) (PeerConn, error)

	// Accept 阻塞等待一个入站的 peer 连接。Transport 关闭后返回错误。
	Accept(ctx context.Context) (PeerConn, error)

	// Addr 返回本 Transport 的监听地址(可用于告知其他 peer 如何连接本节点)。
	Addr() string
}
