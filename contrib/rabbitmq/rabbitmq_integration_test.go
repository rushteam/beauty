package rabbitmq_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/rushteam/beauty/contrib/rabbitmq"
	"github.com/rushteam/beauty/pkg/messaging/mq"
)

func TestIntegrationPubSub(t *testing.T) {
	url := os.Getenv("RABBITMQ_URL")
	if url == "" {
		t.Skip("set RABBITMQ_URL for integration test")
	}

	pub, err := rabbitmq.NewPublisher(url)
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Close()

	sub, err := rabbitmq.NewSubscriber(url)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var got string
	if err := sub.Subscribe(ctx, "demo.topic", func(_ context.Context, msg mq.Message) error {
		mu.Lock()
		got = string(msg.Body)
		mu.Unlock()
		return nil
	}, mq.WithGroup("beauty-itest")); err != nil {
		t.Fatal(err)
	}

	time.Sleep(200 * time.Millisecond)
	if err := pub.Publish(ctx, mq.Message{Topic: "demo.topic", Body: []byte("ping")}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		body := got
		mu.Unlock()
		if body == "ping" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timeout waiting for message")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
