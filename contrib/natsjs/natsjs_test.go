package natsjs_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"

	bjs "github.com/rushteam/beauty/contrib/natsjs"
	"github.com/rushteam/beauty/pkg/mq"
)

// runJS 起一个开启 JetStream 的内嵌 NATS 服务(落盘到临时目录)。
func runJS(t *testing.T) (url string, stop func()) {
	t.Helper()
	opts := &natsserver.Options{
		Host:      "127.0.0.1",
		Port:      -1,
		JetStream: true,
		StoreDir:  t.TempDir(),
	}
	srv, err := natsserver.NewServer(opts)
	if err != nil {
		t.Fatalf("new js server: %v", err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("js server 未就绪")
	}
	return srv.ClientURL(), srv.Shutdown
}

// 持久化 + at-least-once:先发布、后订阅,消息仍被送达(core NATS 会丢,JetStream 不会)。
func TestPersistenceThenSubscribe(t *testing.T) {
	url, stop := runJS(t)
	defer stop()

	conn, err := bjs.Connect(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := conn.EnsureStream(ctx, "JOBS", "jobs"); err != nil {
		t.Fatalf("ensure stream: %v", err)
	}

	// 订阅者还没起,先发 5 条 —— JetStream 落盘保存。
	const n = 5
	for i := range n {
		if err := conn.Publish(ctx, mq.Message{Topic: "jobs", Body: []byte(fmt.Sprintf("j%d", i))}); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	// 之后才订阅(durable,默认 DeliverAll),应收到全部历史消息。
	var got atomic.Int64
	var wg sync.WaitGroup
	wg.Add(n)
	err = conn.Subscribe(ctx, "jobs", func(_ context.Context, _ mq.Message) error {
		got.Add(1)
		wg.Done()
		return nil
	}, mq.WithGroup("workers"))
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("持久化消息未全部送达, 已收 %d/%d", got.Load(), n)
	}
}

// Nak 重投:handler 首次失败,消息被重投,最终成功(at-least-once)。
func TestRedeliveryOnNak(t *testing.T) {
	url, stop := runJS(t)
	defer stop()
	conn, err := bjs.Connect(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := conn.EnsureStream(ctx, "EVT", "evt"); err != nil {
		t.Fatalf("ensure stream: %v", err)
	}

	var attempts atomic.Int64
	succeeded := make(chan struct{})
	var once sync.Once
	err = conn.Subscribe(ctx, "evt", func(_ context.Context, _ mq.Message) error {
		if attempts.Add(1) < 2 {
			return fmt.Errorf("transient failure") // 首次失败 → Nak → 重投
		}
		once.Do(func() { close(succeeded) })
		return nil
	}, mq.WithGroup("evt-workers"))
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	if err := conn.Publish(ctx, mq.Message{Topic: "evt", Body: []byte("x")}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case <-succeeded:
	case <-time.After(10 * time.Second):
		t.Fatalf("重投后应成功, attempts=%d", attempts.Load())
	}
	if attempts.Load() < 2 {
		t.Fatalf("应至少重投一次, attempts=%d", attempts.Load())
	}
}
