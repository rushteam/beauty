package tcpserver_test

import (
	"bufio"
	"context"
	"net"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/service/tcpserver"
)

func TestServer_StartAndConnect(t *testing.T) {
	handler := func(ctx context.Context, conn net.Conn) {
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			line := scanner.Text()
			_, _ = conn.Write([]byte("echo:" + line + "\n"))
		}
	}

	srv := tcpserver.New(":0", handler)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()

	<-srv.Ready()

	// 连接并发送数据
	conn, err := net.DialTimeout("tcp", srv.Addr(), time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_, _ = conn.Write([]byte("hello\n"))
	reply := make([]byte, 64)
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	n, err := conn.Read(reply)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := string(reply[:n]); got != "echo:hello\n" {
		t.Fatalf("unexpected reply: %q", got)
	}
	_ = conn.Close()

	if srv.ActiveConns() < 0 {
		t.Fatal("active conns should not be negative")
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
}

func TestServer_MaxConns(t *testing.T) {
	connected := make(chan struct{})
	handler := func(ctx context.Context, conn net.Conn) {
		connected <- struct{}{}
		<-ctx.Done()
	}

	srv := tcpserver.New(":0", handler, tcpserver.WithMaxConns(1))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Start(ctx) }()
	<-srv.Ready()

	// 第一个连接成功
	conn1, err := net.DialTimeout("tcp", srv.Addr(), time.Second)
	if err != nil {
		t.Fatalf("dial conn1: %v", err)
	}
	defer conn1.Close()
	<-connected

	// 第二个连接应被立即关闭
	conn2, err := net.DialTimeout("tcp", srv.Addr(), time.Second)
	if err != nil {
		t.Fatalf("dial conn2: %v", err)
	}
	defer conn2.Close()
	_ = conn2.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	buf := make([]byte, 1)
	_, err = conn2.Read(buf)
	if err == nil {
		t.Fatal("expected conn2 to be closed by server")
	}
}

func TestServer_GracefulShutdown(t *testing.T) {
	handlerDone := make(chan struct{})
	handler := func(ctx context.Context, conn net.Conn) {
		defer close(handlerDone)
		<-ctx.Done()
	}

	srv := tcpserver.New(":0", handler, tcpserver.WithShutdownTimeout(2*time.Second))

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()
	<-srv.Ready()

	conn, err := net.DialTimeout("tcp", srv.Addr(), time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	time.Sleep(50 * time.Millisecond)

	cancel()

	select {
	case <-handlerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not exit after shutdown")
	}

	if err := <-errCh; err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
}
