package p2p

import (
	"sync"
)

// LocalNetwork 是 Network 的内存实现,管理一组 PeerConn。
// 信令/拓扑层通过 AddPeer/RemovePeer 驱动;业务层通过 Network 接口消费。
type LocalNetwork struct {
	localID PeerID

	mu    sync.RWMutex
	peers map[PeerID]PeerConn

	events chan PeerEvent
	done   chan struct{}
	once   sync.Once
}

// NewLocalNetwork 创建一个本地 P2P 网络实例。eventBuf 决定事件通道缓冲大小。
func NewLocalNetwork(localID PeerID, eventBuf int) *LocalNetwork {
	if eventBuf <= 0 {
		eventBuf = 64
	}
	return &LocalNetwork{
		localID: localID,
		peers:   make(map[PeerID]PeerConn),
		events:  make(chan PeerEvent, eventBuf),
		done:    make(chan struct{}),
	}
}

// LocalID 返回本节点 ID。
func (n *LocalNetwork) LocalID() PeerID { return n.localID }

// Peers 返回当前已连接的所有 peer。
func (n *LocalNetwork) Peers() []PeerConn {
	n.mu.RLock()
	defer n.mu.RUnlock()
	out := make([]PeerConn, 0, len(n.peers))
	for _, p := range n.peers {
		out = append(out, p)
	}
	return out
}

// Peer 按 ID 获取已连接的 peer。
func (n *LocalNetwork) Peer(id PeerID) PeerConn {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.peers[id]
}

// Events 返回 peer 生命周期事件通道。
func (n *LocalNetwork) Events() <-chan PeerEvent { return n.events }

// Broadcast 向所有已连接 peer 广播。
func (n *LocalNetwork) Broadcast(data []byte, reliable bool) error {
	n.mu.RLock()
	peers := make([]PeerConn, 0, len(n.peers))
	for _, p := range n.peers {
		peers = append(peers, p)
	}
	n.mu.RUnlock()

	var firstErr error
	for _, p := range peers {
		var err error
		if reliable {
			err = p.SendReliable(data)
		} else {
			err = p.SendUnreliable(data)
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Close 关闭网络:断开所有 peer、关闭事件通道。幂等。
func (n *LocalNetwork) Close() error {
	n.once.Do(func() {
		close(n.done)
		n.mu.Lock()
		for id, p := range n.peers {
			_ = p.Close()
			delete(n.peers, id)
		}
		n.mu.Unlock()
		close(n.events)
	})
	return nil
}

// AddPeer 将一条已建立的连接登记到网络。由信令/拓扑层调用。
func (n *LocalNetwork) AddPeer(conn PeerConn) {
	id := conn.ID()
	n.mu.Lock()
	n.peers[id] = conn
	n.mu.Unlock()
	n.emit(PeerEvent{PeerID: id, State: PeerStateConnected, Conn: conn})

	go n.watchPeer(conn)
}

// RemovePeer 从网络移除一个 peer(不关闭其连接——由调用方决定是否 Close)。
func (n *LocalNetwork) RemovePeer(id PeerID) {
	n.mu.Lock()
	_, ok := n.peers[id]
	if ok {
		delete(n.peers, id)
	}
	n.mu.Unlock()
	if ok {
		n.emit(PeerEvent{PeerID: id, State: PeerStateDisconnected})
	}
}

func (n *LocalNetwork) watchPeer(conn PeerConn) {
	<-conn.Context().Done()
	n.RemovePeer(conn.ID())
}

func (n *LocalNetwork) emit(e PeerEvent) {
	select {
	case n.events <- e:
	case <-n.done:
	default:
	}
}
