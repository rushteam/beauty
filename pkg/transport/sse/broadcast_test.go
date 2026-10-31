package sse_test

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/messaging/stream"
	"github.com/rushteam/beauty/pkg/transport/sse"
)

func TestBroadcast_FanOutToClients(t *testing.T) {
	b := stream.New[sse.Event](stream.WithBufferSize(16))
	defer b.Close()

	srv := httptest.NewServer(sse.Broadcast(b))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 两个客户端
	read := func() (*bufio.Scanner, func()) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		return bufio.NewScanner(resp.Body), func() { resp.Body.Close() }
	}
	s1, c1 := read()
	defer c1()
	s2, c2 := read()
	defer c2()

	// 等两个订阅都注册上
	deadline := time.Now().Add(2 * time.Second)
	for b.SubscriberCount() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("subscribers not registered, got %d", b.SubscriberCount())
		}
		time.Sleep(5 * time.Millisecond)
	}

	b.Publish(sse.Event{Data: "hello"})

	want := "data: hello"
	for i, s := range []*bufio.Scanner{s1, s2} {
		if !scanForLine(s, want, 2*time.Second) {
			t.Fatalf("client %d did not receive %q", i+1, want)
		}
	}
}

// scanForLine 在 timeout 内扫描到等于 want 的行返回 true。
func scanForLine(s *bufio.Scanner, want string, timeout time.Duration) bool {
	type res struct{ ok bool }
	ch := make(chan res, 1)
	go func() {
		for s.Scan() {
			if strings.TrimSpace(s.Text()) == want {
				ch <- res{true}
				return
			}
		}
		ch <- res{false}
	}()
	select {
	case r := <-ch:
		return r.ok
	case <-time.After(timeout):
		return false
	}
}

func TestStream_KeyedRooms(t *testing.T) {
	rooms := stream.NewHub[string, sse.Event](stream.WithBufferSize(16))
	defer rooms.Close()

	mux := http.NewServeMux()
	mux.Handle("GET /rooms/{room}", sse.Stream(func(r *http.Request) (<-chan sse.Event, func(), error) {
		ch, cancel := rooms.Subscribe(r.Context(), r.PathValue("room"))
		return ch, cancel, nil
	}))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	open := func(room string) *bufio.Scanner {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/rooms/"+room, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return bufio.NewScanner(resp.Body)
	}
	a, b := open("a"), open("b")

	deadline := time.Now().Add(2 * time.Second)
	for rooms.Len() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("rooms not registered, got %d", rooms.Len())
		}
		time.Sleep(5 * time.Millisecond)
	}

	rooms.Publish("b", sse.Event{Data: "to-b"})
	rooms.Publish("a", sse.Event{Data: "to-a"})
	if !scanForLine(a, "data: to-a", 2*time.Second) {
		t.Fatal("room a did not receive its event")
	}
	if !scanForLine(b, "data: to-b", 2*time.Second) {
		t.Fatal("room b did not receive its event")
	}

	cancel()
	deadline = time.Now().Add(2 * time.Second)
	for rooms.Len() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("rooms not reclaimed after disconnect, got %d", rooms.Len())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestBroadcast_UnsubscribeOnDisconnect(t *testing.T) {
	b := stream.New[sse.Event]()
	defer b.Close()
	srv := httptest.NewServer(sse.Broadcast(b))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for b.SubscriberCount() < 1 {
		if time.Now().After(deadline) {
			t.Fatal("subscriber not registered")
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel() // 客户端断开
	resp.Body.Close()

	deadline = time.Now().Add(2 * time.Second)
	for b.SubscriberCount() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("subscriber not cleaned up after disconnect, got %d", b.SubscriberCount())
		}
		time.Sleep(5 * time.Millisecond)
	}
}
