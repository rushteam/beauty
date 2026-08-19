package p2p

import (
	"context"
	"sync"
	"testing"
	"time"
)

// mockPeerConn 用于测试的 PeerConn 实现。
type mockPeerConn struct {
	id      PeerID
	ctx     context.Context
	cancel  context.CancelFunc
	recvCh  chan Message
	sent    [][]byte
	sentUnr [][]byte
	mu      sync.Mutex
}

func newMockPeerConn(id PeerID) *mockPeerConn {
	ctx, cancel := context.WithCancel(context.Background())
	return &mockPeerConn{
		id:     id,
		ctx:    ctx,
		cancel: cancel,
		recvCh: make(chan Message, 16),
	}
}

func (m *mockPeerConn) ID() PeerID               { return m.id }
func (m *mockPeerConn) Context() context.Context { return m.ctx }
func (m *mockPeerConn) Recv() <-chan Message     { return m.recvCh }

func (m *mockPeerConn) SendReliable(data []byte) error {
	m.mu.Lock()
	m.sent = append(m.sent, data)
	m.mu.Unlock()
	return nil
}

func (m *mockPeerConn) SendUnreliable(data []byte) error {
	m.mu.Lock()
	m.sentUnr = append(m.sentUnr, data)
	m.mu.Unlock()
	return nil
}

func (m *mockPeerConn) Close() error {
	m.cancel()
	return nil
}

func TestLocalNetwork_AddRemovePeer(t *testing.T) {
	net := NewLocalNetwork("local", 16)
	defer net.Close()

	peerA := newMockPeerConn("A")
	peerB := newMockPeerConn("B")

	net.AddPeer(peerA)
	net.AddPeer(peerB)

	if len(net.Peers()) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(net.Peers()))
	}
	if net.Peer("A") == nil {
		t.Fatal("expected peer A to exist")
	}

	net.RemovePeer("A")
	if len(net.Peers()) != 1 {
		t.Fatalf("expected 1 peer after remove, got %d", len(net.Peers()))
	}
	if net.Peer("A") != nil {
		t.Fatal("expected peer A to be removed")
	}
}

func TestLocalNetwork_Broadcast(t *testing.T) {
	net := NewLocalNetwork("local", 16)
	defer net.Close()

	peerA := newMockPeerConn("A")
	peerB := newMockPeerConn("B")
	net.AddPeer(peerA)
	net.AddPeer(peerB)

	data := []byte("hello p2p")
	if err := net.Broadcast(data, true); err != nil {
		t.Fatalf("broadcast reliable: %v", err)
	}

	peerA.mu.Lock()
	if len(peerA.sent) != 1 || string(peerA.sent[0]) != "hello p2p" {
		t.Errorf("peer A didn't receive reliable broadcast")
	}
	peerA.mu.Unlock()

	if err := net.Broadcast(data, false); err != nil {
		t.Fatalf("broadcast unreliable: %v", err)
	}
	peerB.mu.Lock()
	if len(peerB.sentUnr) != 1 {
		t.Errorf("peer B didn't receive unreliable broadcast")
	}
	peerB.mu.Unlock()
}

func TestLocalNetwork_Events(t *testing.T) {
	net := NewLocalNetwork("local", 16)
	defer net.Close()

	peer := newMockPeerConn("A")
	net.AddPeer(peer)

	select {
	case ev := <-net.Events():
		if ev.PeerID != "A" || ev.State != PeerStateConnected {
			t.Fatalf("unexpected event: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for connected event")
	}
}

func TestLocalNetwork_WatchPeerDisconnect(t *testing.T) {
	net := NewLocalNetwork("local", 16)
	defer net.Close()

	peer := newMockPeerConn("A")
	net.AddPeer(peer)
	<-net.Events() // consume connected event

	peer.Close()

	select {
	case ev := <-net.Events():
		if ev.PeerID != "A" || ev.State != PeerStateDisconnected {
			t.Fatalf("unexpected event: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for disconnect event")
	}

	if net.Peer("A") != nil {
		t.Fatal("peer A should be auto-removed after disconnect")
	}
}
