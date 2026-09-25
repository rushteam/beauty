package tcpserver_test

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/service/tcpserver"
)

// TestServer_ReadTimeout 验证空闲连接会因读超时被回收(handler 的 Read 返回超时错误)。
func TestServer_ReadTimeout(t *testing.T) {
	readErr := make(chan error, 1)
	handler := func(ctx context.Context, conn net.Conn) {
		buf := make([]byte, 16)
		_, err := conn.Read(buf) // 客户端不发数据,应在 readTimeout 后超时返回
		readErr <- err
	}

	srv := tcpserver.New(":0", handler, tcpserver.WithReadTimeout(150*time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Start(ctx) }()
	<-srv.Ready()

	conn, err := net.DialTimeout("tcp", srv.Addr(), time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	select {
	case err := <-readErr:
		var ne net.Error
		if !errors.As(err, &ne) || !ne.Timeout() {
			t.Fatalf("expected timeout error, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler read did not time out")
	}
}

// TestServer_OnAcceptReject 验证预检拒绝的连接不会进入 handler,且会触发 OnClose。
func TestServer_OnAcceptReject(t *testing.T) {
	var handlerCalled atomic.Bool
	var closed atomic.Bool
	handler := func(ctx context.Context, conn net.Conn) { handlerCalled.Store(true) }

	srv := tcpserver.New(":0", handler,
		tcpserver.WithOnAccept(func(_ context.Context, _ net.Conn) error {
			return errors.New("rejected")
		}),
		tcpserver.WithOnClose(func(_ net.Conn) { closed.Store(true) }),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Start(ctx) }()
	<-srv.Ready()

	conn, err := net.DialTimeout("tcp", srv.Addr(), time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// 被拒绝的连接应被服务端关闭(读到 EOF)。
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 1)
	if _, err := conn.Read(buf); err == nil {
		t.Fatal("expected connection to be closed by server")
	}

	if handlerCalled.Load() {
		t.Error("handler should not be called for rejected connection")
	}
	// OnClose 在预检拒绝后也应被调用。
	waitTrue(t, closed.Load, "OnClose not called after reject")
}

// TestServer_OnAcceptAllowAndOnClose 验证放行连接进入 handler,结束后触发 OnClose。
func TestServer_OnAcceptAllowAndOnClose(t *testing.T) {
	var accepted atomic.Int64
	var closed atomic.Int64
	served := make(chan struct{})
	handler := func(ctx context.Context, conn net.Conn) { close(served) }

	srv := tcpserver.New(":0", handler,
		tcpserver.WithOnAccept(func(_ context.Context, _ net.Conn) error {
			accepted.Add(1)
			return nil
		}),
		tcpserver.WithOnClose(func(_ net.Conn) { closed.Add(1) }),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Start(ctx) }()
	<-srv.Ready()

	conn, err := net.DialTimeout("tcp", srv.Addr(), time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	select {
	case <-served:
	case <-time.After(2 * time.Second):
		t.Fatal("handler was not invoked for accepted connection")
	}
	if accepted.Load() != 1 {
		t.Errorf("onAccept called %d times, want 1", accepted.Load())
	}
	waitTrue(t, func() bool { return closed.Load() == 1 }, "OnClose not called once after handler returned")
}

func waitTrue(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(msg)
}
