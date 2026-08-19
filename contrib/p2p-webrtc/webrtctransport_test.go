package webrtctransport

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/rushteam/beauty/pkg/transport/p2p"
)

func TestWebRTCTransport_P2P(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var mu sync.Mutex
	// 用内存信令:直接调用对端的 HandleSignal
	var tA, tB *Transport

	signalA := func(ctx context.Context, target p2p.PeerID, msg SignalMessage) error {
		mu.Lock()
		b := tB
		mu.Unlock()
		if b == nil {
			return nil
		}
		return b.HandleSignal(ctx, "alice", msg)
	}
	signalB := func(ctx context.Context, target p2p.PeerID, msg SignalMessage) error {
		mu.Lock()
		a := tA
		mu.Unlock()
		if a == nil {
			return nil
		}
		return a.HandleSignal(ctx, "bob", msg)
	}

	mu.Lock()
	tA = New("alice", signalA, WithICEServers([]webrtc.ICEServer{}))
	tB = New("bob", signalB, WithICEServers([]webrtc.ICEServer{}))
	mu.Unlock()

	defer tA.Close()
	defer tB.Close()

	// Alice 发起连接 Bob
	var connA p2p.PeerConn
	var connErr error
	done := make(chan struct{})
	go func() {
		connA, connErr = tA.Connect(ctx, "bob")
		close(done)
	}()

	// Bob 接受连接
	connB, err := tB.Accept(ctx)
	if err != nil {
		t.Fatal("accept:", err)
	}
	defer connB.Close()

	<-done
	if connErr != nil {
		t.Fatal("connect:", connErr)
	}
	defer connA.Close()

	if connB.ID() != "alice" {
		t.Fatalf("expected peer ID 'alice', got %q", connB.ID())
	}
	if connA.ID() != "bob" {
		t.Fatalf("expected peer ID 'bob', got %q", connA.ID())
	}

	// 测试可靠通道
	if err := connA.SendReliable([]byte("hello-reliable")); err != nil {
		t.Fatal("send reliable:", err)
	}

	select {
	case msg := <-connB.Recv():
		if string(msg.Data) != "hello-reliable" {
			t.Fatalf("expected 'hello-reliable', got %q", msg.Data)
		}
		if !msg.Reliable {
			t.Fatal("expected reliable=true")
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for reliable message")
	}

	// 测试不可靠通道
	if err := connB.SendUnreliable([]byte("hello-unreliable")); err != nil {
		t.Fatal("send unreliable:", err)
	}

	select {
	case msg := <-connA.Recv():
		if string(msg.Data) != "hello-unreliable" {
			t.Fatalf("expected 'hello-unreliable', got %q", msg.Data)
		}
		if msg.Reliable {
			t.Fatal("expected reliable=false")
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for unreliable message")
	}
}
