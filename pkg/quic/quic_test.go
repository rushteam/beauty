package quic_test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/quic"
)

// startEchoServer 起一个回显 server:回显所有数据报,并对每条流读到 EOF 后回写。
func startEchoServer(t *testing.T, opts ...quic.Option) (addr string, stop func()) {
	t.Helper()
	srv := quic.NewServer("127.0.0.1:0", func(ctx context.Context, c *quic.Conn) error {
		// 数据报回显。
		go func() {
			for {
				b, err := c.ReceiveDatagram(ctx)
				if err != nil {
					return
				}
				_ = c.SendDatagram(b)
			}
		}()
		// 流回显:每条流读到发送方 Close(EOF)后原样写回。
		for {
			st, err := c.AcceptStream(ctx)
			if err != nil {
				return err
			}
			go func() {
				data, _ := io.ReadAll(st)
				_, _ = st.Write(data)
				_ = st.Close()
			}()
		}
	}, append([]quic.Option{quic.WithServiceName("test-echo")}, opts...)...)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Start(ctx) }()
	select {
	case <-srv.Ready():
	case <-time.After(3 * time.Second):
		t.Fatal("server 未就绪")
	}
	return srv.Addr().String(), func() {
		cancel()
		<-done
	}
}

func dialClient(t *testing.T, ctx context.Context, addr string) *quic.Conn {
	t.Helper()
	c, err := quic.Dial(ctx, addr, quic.WithInsecureSkipVerify(true))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return c
}

func TestStream_Roundtrip(t *testing.T) {
	addr, stop := startEchoServer(t)
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := dialClient(t, ctx, addr)
	defer c.Close("bye")

	st, err := c.OpenStream(ctx)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	want := []byte("hello-reliable-stream")
	if _, err := st.Write(want); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = st.Close() // 关发送方向 → server 侧读到 EOF 后回写

	got, err := io.ReadAll(st)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("stream echo = %q, want %q", got, want)
	}
}

// TestServer_WithPacketConn 走「自备(缓冲调优的)UDP socket」路径,验证端到端可用。
func TestServer_WithPacketConn(t *testing.T) {
	pc, err := quic.ListenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer pc.Close()

	addr, stop := startEchoServer(t, quic.WithPacketConn(pc))
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := dialClient(t, ctx, addr)
	defer c.Close("bye")

	st, err := c.OpenStream(ctx)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	want := []byte("over-tuned-socket")
	if _, err := st.Write(want); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = st.Close()
	got, err := io.ReadAll(st)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("echo = %q, want %q", got, want)
	}
}

func TestDatagram_Roundtrip(t *testing.T) {
	addr, stop := startEchoServer(t)
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := dialClient(t, ctx, addr)
	defer c.Close("bye")

	want := []byte("hello-unreliable-datagram")
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if err := c.SendDatagram(want); err != nil {
			t.Fatalf("send datagram: %v", err)
		}
		rctx, rcancel := context.WithTimeout(ctx, 200*time.Millisecond)
		got, err := c.ReceiveDatagram(rctx)
		rcancel()
		if err == nil && string(got) == string(want) {
			return // 收到回显,通过
		}
	}
	t.Fatal("未在期限内收到数据报回显")
}
