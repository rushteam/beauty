package ws_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/rushteam/beauty/pkg/stream"
	"github.com/rushteam/beauty/pkg/ws"
)

type notice struct {
	Msg string `json:"msg"`
}

func TestBroadcastJSON_FanOut(t *testing.T) {
	b := stream.New[notice](stream.WithBufferSize(16))
	defer b.Close()

	srv := httptest.NewServer(ws.BroadcastJSON(b))
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http")

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer dialCancel()
	c1, _, err := websocket.Dial(dialCtx, url, nil)
	if err != nil {
		t.Fatalf("dial1: %v", err)
	}
	defer c1.CloseNow()
	c2, _, err := websocket.Dial(dialCtx, url, nil)
	if err != nil {
		t.Fatalf("dial2: %v", err)
	}
	defer c2.CloseNow()

	deadline := time.Now().Add(2 * time.Second)
	for b.SubscriberCount() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("subscribers not registered, got %d", b.SubscriberCount())
		}
		time.Sleep(5 * time.Millisecond)
	}

	b.Publish(notice{Msg: "ping"})

	for i, c := range []*websocket.Conn{c1, c2} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		var got notice
		err := wsjson.Read(ctx, c, &got)
		cancel()
		if err != nil {
			t.Fatalf("client %d read: %v", i+1, err)
		}
		if got.Msg != "ping" {
			t.Fatalf("client %d got %q", i+1, got.Msg)
		}
	}
}

func TestBroadcastJSON_UnsubscribeOnClose(t *testing.T) {
	b := stream.New[notice]()
	defer b.Close()
	srv := httptest.NewServer(ws.BroadcastJSON(b))
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http")

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer dialCancel()
	c, _, err := websocket.Dial(dialCtx, url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for b.SubscriberCount() < 1 {
		if time.Now().After(deadline) {
			t.Fatal("subscriber not registered")
		}
		time.Sleep(5 * time.Millisecond)
	}

	c.Close(websocket.StatusNormalClosure, "")

	deadline = time.Now().Add(2 * time.Second)
	for b.SubscriberCount() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("subscriber not cleaned up, got %d", b.SubscriberCount())
		}
		time.Sleep(5 * time.Millisecond)
	}
}
