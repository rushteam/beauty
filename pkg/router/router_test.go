package router

import (
	"sync/atomic"
	"testing"

	"github.com/rushteam/beauty/pkg/presence"
)

type fakeRegistry struct {
	sinks map[string]Sink
}

func (f *fakeRegistry) Lookup(sid string) Sink { return f.sinks[sid] }

func TestRouter_SendToSessionIDs(t *testing.T) {
	var got atomic.Int32
	regs := &fakeRegistry{sinks: map[string]Sink{
		"s1": func(m Message) bool { got.Add(1); return true },
		"s2": func(m Message) bool { got.Add(1); return true },
		"s3": nil, // 下线
	}}
	r := New(regs, nil)
	n := r.SendToSessionIDs([]string{"s1", "s2", "s3", "s4"}, Message{Data: []byte("hi")})
	if n != 2 {
		t.Fatalf("delivered=%d want 2", n)
	}
	if got.Load() != 2 {
		t.Fatalf("got=%d want 2", got.Load())
	}
}

func TestRouter_SendToStream(t *testing.T) {
	tr := presence.New(nil, 16)
	st := presence.Stream{Mode: 1, Subject: "r"}
	tr.Track("s1", st, presence.Meta{UserID: "u1"})
	tr.Track("s2", st, presence.Meta{UserID: "u2", Hidden: true})

	var got atomic.Int32
	regs := &fakeRegistry{sinks: map[string]Sink{
		"s1": func(m Message) bool { got.Add(1); return true },
		"s2": func(m Message) bool { got.Add(1); return true },
	}}
	r := New(regs, tr)

	// 不含隐藏:1 个。
	n := r.SendToStream(st, Message{Data: []byte("x")}, false)
	if n != 1 {
		t.Fatalf("delivered=%d want 1 (hidden excluded)", n)
	}
	// 含隐藏:2 个。
	n = r.SendToStream(st, Message{Data: []byte("x")}, true)
	if n != 2 {
		t.Fatalf("delivered=%d want 2", n)
	}
}

func TestRouter_DeferredBatching(t *testing.T) {
	var got atomic.Int32
	regs := &fakeRegistry{sinks: map[string]Sink{
		"s1": func(m Message) bool { got.Add(1); return true },
	}}
	r := New(regs, nil)
	// 攒 3 条给同一 session。
	r.QueueDeferred([]string{"s1"}, Message{Data: []byte("a")})
	r.QueueDeferred([]string{"s1"}, Message{Data: []byte("b")})
	r.QueueDeferred([]string{"s1"}, Message{Data: []byte("c")})
	n := r.FlushDeferred()
	if n != 3 {
		t.Fatalf("delivered=%d want 3", n)
	}
	if got.Load() != 3 {
		t.Fatalf("got=%d want 3", got.Load())
	}
}
