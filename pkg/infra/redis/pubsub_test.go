package redis

import (
	"context"
	"errors"
	"maps"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/rushteam/beauty/pkg/messaging/mq"
)

func TestEnvelope_RoundTrip(t *testing.T) {
	cases := []mq.Message{
		{Topic: "t", Body: []byte("plain")},
		{Topic: "t", Key: "user-1", Body: []byte(`{"a":1}`)},
		{Topic: "t", Headers: map[string]string{"trace": "abc", "": "empty-key"}, Body: nil},
		{Topic: "t", Key: "k", Headers: map[string]string{"x": ""}, Body: []byte{0, 1, 2}},
	}
	for _, in := range cases {
		out := decodeMessage(in.Topic, string(encodeMessage(in)))
		if out.Key != in.Key || string(out.Body) != string(in.Body) || !maps.Equal(out.Headers, in.Headers) {
			t.Fatalf("round trip mismatch:\n in=%+v\nout=%+v", in, out)
		}
	}
}

// 无 Key / Headers 时负载就是 Body 原文，非 beauty 发布方的负载也按原文解析。
func TestEnvelope_RawInterop(t *testing.T) {
	if got := string(encodeMessage(mq.Message{Body: []byte("hello")})); got != "hello" {
		t.Fatalf("raw payload = %q, want hello", got)
	}
	if m := decodeMessage("t", `{"text":"hi"}`); string(m.Body) != `{"text":"hi"}` || m.Key != "" || m.Headers != nil {
		t.Fatalf("foreign payload decoded as %+v", m)
	}
}

func TestEnvelope_MalformedFallsBackToRaw(t *testing.T) {
	full := string(encodeMessage(mq.Message{Key: "key", Headers: map[string]string{"h": "v"}, Body: []byte("b")}))
	for i := len(envelopeMagic); i < len(full)-1; i++ {
		truncated := full[:i]
		m := decodeMessage("t", truncated)
		if m.Key == "key" && maps.Equal(m.Headers, map[string]string{"h": "v"}) && string(m.Body) == "b" {
			t.Fatalf("truncated payload %q decoded as complete message", truncated)
		}
	}
	bogus := envelopeMagic + "\xff\xff\xff\xff\xff\xff\xff\xff\xff\x01"
	if m := decodeMessage("t", bogus); string(m.Body) != bogus {
		t.Fatalf("bogus envelope should fall back to raw body, got %+v", m)
	}
}

func TestPubSub_GroupNotSupported(t *testing.T) {
	c := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	defer c.Close()
	p := NewPubSub(c)
	defer p.Close()
	err := p.Subscribe(context.Background(), "t", func(context.Context, mq.Message) error { return nil }, mq.WithGroup("g"))
	if !errors.Is(err, ErrGroupNotSupported) {
		t.Fatalf("err = %v, want ErrGroupNotSupported", err)
	}
}

func TestPubSub_Closed(t *testing.T) {
	c := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	defer c.Close()
	p := NewPubSub(c)
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.Publish(context.Background(), mq.Message{Topic: "t"}); !errors.Is(err, mq.ErrClosed) {
		t.Fatalf("Publish after Close: %v", err)
	}
	if err := p.Subscribe(context.Background(), "t", func(context.Context, mq.Message) error { return nil }); !errors.Is(err, mq.ErrClosed) {
		t.Fatalf("Subscribe after Close: %v", err)
	}
}
