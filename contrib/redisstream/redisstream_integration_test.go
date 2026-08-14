package redisstream_test

import (
	"context"
	"sync"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/rushteam/beauty/contrib/redisstream"
	"github.com/rushteam/beauty/pkg/mq"
)

func TestIntegrationPubSubGroup(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	pub := redisstream.NewPublisher(rdb)
	sub := redisstream.NewSubscriber(rdb, redisstream.WithBlockTime(50*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var got []string
	err = sub.Subscribe(ctx, "events", func(_ context.Context, msg mq.Message) error {
		mu.Lock()
		got = append(got, string(msg.Body))
		mu.Unlock()
		return nil
	}, mq.WithGroup("workers"))
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(20 * time.Millisecond)
	if err := pub.Publish(ctx, mq.Message{Topic: "events", Body: []byte("hello")}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timeout waiting for message")
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if got[0] != "hello" {
		t.Fatalf("body = %q", got[0])
	}
	cancel()
	sub.Close()
}
