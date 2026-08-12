package quictransport

import (
	"context"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/p2p"
	"github.com/rushteam/beauty/pkg/quic"
)

func TestQUICTransport_ConnectAndSend(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 启动 Alice
	tA, err := New("alice", ":0", []quic.Option{}, WithDialOptions(quic.WithInsecureSkipVerify(true)))
	if err != nil {
		t.Fatal(err)
	}
	defer tA.Close()

	// 启动 Bob,知道 Alice 的地址
	tB, err := New("bob", ":0", []quic.Option{},
		WithAddressBook(map[p2p.PeerID]string{"alice": tA.Addr()}),
		WithDialOptions(quic.WithInsecureSkipVerify(true)),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer tB.Close()

	// Bob 连接 Alice
	accepted := make(chan p2p.PeerConn, 1)
	go func() {
		conn, err := tA.Accept(ctx)
		if err != nil {
			t.Error("accept:", err)
			return
		}
		accepted <- conn
	}()

	connB, err := tB.Connect(ctx, "alice")
	if err != nil {
		t.Fatal("connect:", err)
	}
	defer connB.Close()

	connA := <-accepted
	defer connA.Close()

	if connA.ID() != "bob" {
		t.Fatalf("expected 'bob', got %q", connA.ID())
	}

	// 测试可靠通道(QUIC stream)
	if err := connB.SendReliable([]byte("reliable-msg")); err != nil {
		t.Fatal("send reliable:", err)
	}

	select {
	case msg := <-connA.Recv():
		if string(msg.Data) != "reliable-msg" {
			t.Fatalf("expected 'reliable-msg', got %q", msg.Data)
		}
		if !msg.Reliable {
			t.Fatal("expected reliable=true")
		}
	case <-ctx.Done():
		t.Fatal("timeout")
	}

	// 测试不可靠通道(QUIC datagram)
	if err := connA.SendUnreliable([]byte("datagram-msg")); err != nil {
		t.Fatal("send unreliable:", err)
	}

	select {
	case msg := <-connB.Recv():
		if string(msg.Data) != "datagram-msg" {
			t.Fatalf("expected 'datagram-msg', got %q", msg.Data)
		}
		if msg.Reliable {
			t.Fatal("expected reliable=false")
		}
	case <-ctx.Done():
		t.Fatal("timeout")
	}
}
