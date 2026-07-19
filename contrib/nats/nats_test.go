package nats_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"

	bnats "github.com/rushteam/beauty/contrib/nats"
	"github.com/rushteam/beauty/pkg/mq"
)

// runServer 起一个内嵌 NATS 服务,返回其客户端地址与关闭函数。
func runServer(t *testing.T) (url string, stop func()) {
	t.Helper()
	opts := &natsserver.Options{Host: "127.0.0.1", Port: -1} // -1 随机端口
	srv, err := natsserver.NewServer(opts)
	if err != nil {
		t.Fatalf("new nats server: %v", err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats server 未就绪")
	}
	return srv.ClientURL(), srv.Shutdown
}

// 扇出:两个普通订阅者都收到,且 Headers/Key 透传。
func TestPublishSubscribe_Fanout(t *testing.T) {
	url, stop := runServer(t)
	defer stop()

	conn, err := bnats.Connect(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	got := make(chan mq.Message, 2)
	for range 2 {
		if err := conn.Subscribe(ctx, "evt", func(_ context.Context, m mq.Message) error {
			got <- m
			return nil
		}); err != nil {
			t.Fatalf("subscribe: %v", err)
		}
	}
	// 等订阅在服务端注册完成。
	time.Sleep(100 * time.Millisecond)

	err = conn.Publish(ctx, mq.Message{
		Topic:   "evt",
		Key:     "user-1",
		Body:    []byte("hello"),
		Headers: map[string]string{"content-type": "text/plain"},
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	for range 2 {
		select {
		case m := <-got:
			if string(m.Body) != "hello" || m.Key != "user-1" || m.Headers["content-type"] != "text/plain" {
				t.Fatalf("消息透传错误: %+v", m)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("扇出未送达两个订阅者")
		}
	}
}

// 队列组:同组竞争消费,每条只投一个,合计收全。
func TestQueueGroup(t *testing.T) {
	url, stop := runServer(t)
	defer stop()
	conn, err := bnats.Connect(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const n = 50
	var total atomic.Int64
	var wg sync.WaitGroup
	wg.Add(n)
	h := func(_ context.Context, _ mq.Message) error { total.Add(1); wg.Done(); return nil }
	for range 3 {
		if err := conn.Subscribe(ctx, "jobs", h, mq.WithGroup("workers")); err != nil {
			t.Fatalf("subscribe: %v", err)
		}
	}
	time.Sleep(100 * time.Millisecond)

	for i := range n {
		if err := conn.Publish(ctx, mq.Message{Topic: "jobs", Body: []byte{byte(i)}}); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("队列组消费超时, 已收 %d/%d", total.Load(), n)
	}
	if total.Load() != n {
		t.Fatalf("队列组合计 = %d, want %d(不重不漏)", total.Load(), n)
	}
}

// ctx 取消后退订,不再收消息。
func TestUnsubscribeOnCtx(t *testing.T) {
	url, stop := runServer(t)
	defer stop()
	conn, err := bnats.Connect(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	subCtx, unsub := context.WithCancel(context.Background())
	var got atomic.Int64
	recv := make(chan struct{}, 1)
	_ = conn.Subscribe(subCtx, "t", func(_ context.Context, _ mq.Message) error {
		got.Add(1)
		select {
		case recv <- struct{}{}:
		default:
		}
		return nil
	})
	time.Sleep(100 * time.Millisecond)

	_ = conn.Publish(context.Background(), mq.Message{Topic: "t", Body: []byte("1")})
	select {
	case <-recv:
	case <-time.After(3 * time.Second):
		t.Fatal("首条未送达")
	}

	unsub()
	time.Sleep(150 * time.Millisecond)
	_ = conn.Publish(context.Background(), mq.Message{Topic: "t", Body: []byte("2")})
	time.Sleep(150 * time.Millisecond)
	if got.Load() != 1 {
		t.Fatalf("退订后仍收到, got=%d want 1", got.Load())
	}
}
