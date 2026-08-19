package tcptransport

import (
	"context"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/transport/p2p"
)

func TestTCPTransport_ConnectAndSend(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 启动两个 transport
	tA, err := New("alice", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer tA.Close()

	tB, err := New("bob", ":0", WithAddressBook(map[p2p.PeerID]string{
		"alice": tA.Addr(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer tB.Close()

	// B 主动连接 A
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
		t.Fatalf("expected peer ID 'bob', got %q", connA.ID())
	}
	if connB.ID() != "alice" {
		t.Fatalf("expected peer ID 'alice', got %q", connB.ID())
	}

	// 测试可靠通道
	if err := connB.SendReliable([]byte("hello")); err != nil {
		t.Fatal("send reliable:", err)
	}

	select {
	case msg := <-connA.Recv():
		if string(msg.Data) != "hello" {
			t.Fatalf("expected 'hello', got %q", msg.Data)
		}
		if !msg.Reliable {
			t.Fatal("expected reliable message")
		}
		if msg.From != "bob" {
			t.Fatalf("expected from 'bob', got %q", msg.From)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for message")
	}

	// 测试不可靠通道(TCP 下降级为可靠)
	if err := connA.SendUnreliable([]byte("world")); err != nil {
		t.Fatal("send unreliable:", err)
	}

	select {
	case msg := <-connB.Recv():
		if string(msg.Data) != "world" {
			t.Fatalf("expected 'world', got %q", msg.Data)
		}
		if msg.Reliable {
			t.Fatal("expected unreliable flag")
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for message")
	}
}

func TestTCPTransport_CloseDisconnects(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tA, err := New("alice", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer tA.Close()

	tB, err := New("bob", ":0", WithAddressBook(map[p2p.PeerID]string{
		"alice": tA.Addr(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer tB.Close()

	go func() {
		conn, _ := tA.Accept(ctx)
		if conn != nil {
			// 接受后立刻关闭
			conn.Close()
		}
	}()

	connB, err := tB.Connect(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}

	// 等待对端关闭导致 context 取消
	select {
	case <-connB.Context().Done():
		// 预期行为:对端关闭后 context 取消
	case <-time.After(3 * time.Second):
		t.Fatal("timeout: peer close did not cancel context")
	}
}

func TestTCPTransport_SetAddr(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tA, err := New("alice", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer tA.Close()

	tB, err := New("bob", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer tB.Close()

	// 动态添加地址
	tB.SetAddr("alice", tA.Addr())

	go func() { tA.Accept(ctx) }()

	conn, err := tB.Connect(ctx, "alice")
	if err != nil {
		t.Fatal("connect after SetAddr:", err)
	}
	conn.Close()
}
