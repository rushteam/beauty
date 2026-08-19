// Package topology 提供 P2P 网络的拓扑策略:当 peer 加入/离开房间时,决定哪些 peer
// 之间应建立直连。策略是纯函数,不持有状态——由信令服务(pkg/p2p/signaling)调用。
//
// 内置策略:
//   - FullMesh:全连接,每两个 peer 互相连接(适合 ≤8 人的小规模场景)
//   - Star:星型拓扑,所有 peer 只与一个中心节点连接(适合主从模式)
//   - ClientServer:动态选主,第一个加入的自动成为 host(适合开房间)
//   - MatchPairs:配对模式,每两个 peer 按到达顺序 1v1 配对(适合对战匹配)
//   - WaitForN:凑够 N 人后一次性触发全连接(适合多人对战匹配)
//
// 自定义策略:实现 Topology 接口即可。
package topology

import (
	"sync"

	"github.com/rushteam/beauty/pkg/transport/p2p"
)

// PeerPair 表示一对需要建立连接的 peer:Initiator 负责发起 offer。
type PeerPair struct {
	Initiator p2p.PeerID // 主动方:生成并发送 offer
	Responder p2p.PeerID // 被动方:收到 offer 后返回 answer
}

// Topology 定义拓扑策略:给定房间当前成员和新加入的 peer,返回需要建立的连接对。
type Topology interface {
	// OnPeerJoin 当新 peer 加入时,返回需要建立的连接对。
	// existingPeers 不含 newPeer。
	OnPeerJoin(newPeer p2p.PeerID, existingPeers []p2p.PeerID) []PeerPair

	// OnPeerLeave 当 peer 离开时,返回需要断开的连接对(可选——大多数实现由底层
	// 连接断开自动清理,返回 nil 即可)。
	OnPeerLeave(leavingPeer p2p.PeerID, remainingPeers []p2p.PeerID) []PeerPair
}

// ─── FullMesh ───

// FullMesh 全连接拓扑:新 peer 与所有已有 peer 建立连接。
// 总连接数 = n*(n-1)/2,适合小规模(≤8 人)。
type FullMesh struct{}

func (FullMesh) OnPeerJoin(newPeer p2p.PeerID, existing []p2p.PeerID) []PeerPair {
	pairs := make([]PeerPair, 0, len(existing))
	for _, ep := range existing {
		pairs = append(pairs, PeerPair{
			Initiator: ep,
			Responder: newPeer,
		})
	}
	return pairs
}

func (FullMesh) OnPeerLeave(_ p2p.PeerID, _ []p2p.PeerID) []PeerPair { return nil }

// ─── Star ───

// Star 星型拓扑:所有 peer 只与指定的 hub 节点连接。
// hub 不在线时新 peer 无法建连——适合"主机-客户端"或"房主"模式(hub ID 预知)。
type Star struct {
	Hub p2p.PeerID
}

func (s Star) OnPeerJoin(newPeer p2p.PeerID, existing []p2p.PeerID) []PeerPair {
	if newPeer == s.Hub {
		pairs := make([]PeerPair, 0, len(existing))
		for _, ep := range existing {
			pairs = append(pairs, PeerPair{Initiator: s.Hub, Responder: ep})
		}
		return pairs
	}
	for _, ep := range existing {
		if ep == s.Hub {
			return []PeerPair{{Initiator: s.Hub, Responder: newPeer}}
		}
	}
	return nil
}

func (Star) OnPeerLeave(_ p2p.PeerID, _ []p2p.PeerID) []PeerPair { return nil }

// ─── ClientServer (动态选主) ───

// ClientServer 动态选主拓扑:第一个加入房间的 peer 自动成为 Host,
// 后续 peer 只与 Host 建连。无需预知 Host ID。
//
// Host 离开后房间内所有 client 的连接自然断开(底层 PeerConnection 关闭)。
// 如需"主机迁移"(host 断开后下一个接任),在信令层 OnHostDisconnected 回调中处理。
type ClientServer struct {
	mu   sync.Mutex
	host p2p.PeerID
}

// NewClientServer 创建一个动态选主拓扑实例。
func NewClientServer() *ClientServer { return &ClientServer{} }

// Host 返回当前 host 的 ID(空串表示还没人加入)。
func (cs *ClientServer) Host() p2p.PeerID {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.host
}

func (cs *ClientServer) OnPeerJoin(newPeer p2p.PeerID, existing []p2p.PeerID) []PeerPair {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if cs.host == "" {
		cs.host = newPeer
		// 如果已有人在(不应该,但防御性处理),全部与新 host 连
		pairs := make([]PeerPair, 0, len(existing))
		for _, ep := range existing {
			pairs = append(pairs, PeerPair{Initiator: newPeer, Responder: ep})
		}
		return pairs
	}
	// host 已存在,新 peer 只与 host 连
	return []PeerPair{{Initiator: cs.host, Responder: newPeer}}
}

func (cs *ClientServer) OnPeerLeave(leavingPeer p2p.PeerID, _ []p2p.PeerID) []PeerPair {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.host == leavingPeer {
		cs.host = "" // host 离开,清空(由上层决定是否迁移)
	}
	return nil
}

// ─── WaitForN (凑人配对) ───

// WaitForN 凑够 N 人后一次性触发全连接。适合多人对战匹配(如 4 人一局)。
// 有状态——与纯 Topology 接口一致,但内部维护等待队列。
//
// 用法(URL 路由 ?next=N):
//
//	topo := topology.NewWaitForN(4) // 凑够 4 人
//	sig := signaling.NewServer(signaling.WithTopology(topo))
type WaitForN struct {
	n       int
	mu      sync.Mutex
	waiting []p2p.PeerID
}

// NewWaitForN 创建一个凑人拓扑。n 为每组人数(≥2)。
func NewWaitForN(n int) *WaitForN {
	if n < 2 {
		n = 2
	}
	return &WaitForN{n: n}
}

func (w *WaitForN) OnPeerJoin(newPeer p2p.PeerID, _ []p2p.PeerID) []PeerPair {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.waiting = append(w.waiting, newPeer)
	if len(w.waiting) < w.n {
		return nil // 还没凑够
	}

	// 凑够了:取出 N 个人,生成全连接
	group := make([]p2p.PeerID, w.n)
	copy(group, w.waiting[:w.n])
	w.waiting = w.waiting[w.n:]

	pairs := make([]PeerPair, 0, w.n*(w.n-1)/2)
	for i := 0; i < len(group); i++ {
		for j := i + 1; j < len(group); j++ {
			pairs = append(pairs, PeerPair{Initiator: group[i], Responder: group[j]})
		}
	}
	return pairs
}

func (w *WaitForN) OnPeerLeave(leavingPeer p2p.PeerID, _ []p2p.PeerID) []PeerPair {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i, id := range w.waiting {
		if id == leavingPeer {
			w.waiting = append(w.waiting[:i], w.waiting[i+1:]...)
			break
		}
	}
	return nil
}

// WaitingCount 返回当前等待中的 peer 数量。
func (w *WaitForN) WaitingCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.waiting)
}

// ─── MatchPairs ───

// MatchPairs 配对拓扑:peer 按到达顺序两两配对(1v1)。等价于 WaitForN(2)。
type MatchPairs struct{}

func (MatchPairs) OnPeerJoin(_ p2p.PeerID, _ []p2p.PeerID) []PeerPair  { return nil }
func (MatchPairs) OnPeerLeave(_ p2p.PeerID, _ []p2p.PeerID) []PeerPair { return nil }

// Queue 是 MatchPairs 的有状态配对器:维护等待队列,每凑够一对就输出。
// 比纯 Topology 接口更实用——信令层直接用 Queue 做匹配。
type Queue struct {
	waiting []p2p.PeerID
}

// NewQueue 创建配对队列。
func NewQueue() *Queue { return &Queue{} }

// Enqueue 将 peer 加入等待队列。如果配对成功返回对应的 PeerPair,否则返回 nil。
func (q *Queue) Enqueue(peerID p2p.PeerID) *PeerPair {
	if len(q.waiting) > 0 {
		partner := q.waiting[0]
		q.waiting = q.waiting[1:]
		return &PeerPair{Initiator: partner, Responder: peerID}
	}
	q.waiting = append(q.waiting, peerID)
	return nil
}

// Dequeue 从等待队列移除(peer 离开时清理)。
func (q *Queue) Dequeue(peerID p2p.PeerID) {
	for i, id := range q.waiting {
		if id == peerID {
			q.waiting = append(q.waiting[:i], q.waiting[i+1:]...)
			return
		}
	}
}

// WaitingCount 返回当前等待配对的 peer 数量。
func (q *Queue) WaitingCount() int { return len(q.waiting) }
