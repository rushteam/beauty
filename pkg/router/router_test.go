package router

import (
	"sync"
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

// fakeForwarder 记录跨节点转发调用。
type fakeForwarder struct {
	mu   sync.Mutex
	calls []forwardCall
}

type forwardCall struct {
	node string
	nIDs int
	msg  Message
}

func (f *fakeForwarder) Forward(node string, ids []presence.ID, m Message) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, forwardCall{node: node, nIDs: len(ids), msg: m})
	// 模拟远端全部成功。
	return len(ids)
}

func (f *fakeForwarder) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func TestRouter_CrossNode_Forward(t *testing.T) {
	var localGot atomic.Int32
	regs := &fakeRegistry{sinks: map[string]Sink{
		"s1": func(m Message) bool { localGot.Add(1); return true },
		"s5": func(m Message) bool { localGot.Add(1); return true },
	}}
	fwd := &fakeForwarder{}
	r := New(regs, nil,
		WithLocalNode("node-a"),
		WithForwarder(fwd),
	)
	st := presence.Stream{Mode: 1, Subject: "r"}
	ids := []presence.ID{
		{SessionID: "s1", Stream: st, Node: "node-a"},        // 本地
		{SessionID: "s2", Stream: st, Node: "node-b"},        // 远端
		{SessionID: "s3", Stream: st, Node: "node-b"},        // 远端(同节点攒批)
		{SessionID: "s4", Stream: st, Node: "node-c"},        // 远端(另一节点)
		{SessionID: "s5", Stream: st},                         // Node 空=本地
	}
	n := r.SendToPresenceIDs(ids, Message{Data: []byte("x")})
	// 本地 2(s1+s5)+ 远端 3(s2+s3+s4)= 5。
	if n != 5 {
		t.Fatalf("delivered=%d want 5", n)
	}
	if localGot.Load() != 2 {
		t.Fatalf("local got=%d want 2", localGot.Load())
	}
	// 远端按节点分组:node-b 一批 2 个,node-c 一批 1 个。
	if fwd.count() != 2 {
		t.Fatalf("forward calls=%d want 2", fwd.count())
	}
}

func TestRouter_CrossNode_NoForwarder_Drops(t *testing.T) {
	regs := &fakeRegistry{sinks: map[string]Sink{
		"s1": func(m Message) bool { return true },
	}}
	r := New(regs, nil, WithLocalNode("node-a")) // 无 Forwarder
	st := presence.Stream{Mode: 1, Subject: "r"}
	ids := []presence.ID{
		{SessionID: "s1", Stream: st, Node: "node-a"}, // 本地
		{SessionID: "s2", Stream: st, Node: "node-b"}, // 远端但无 Forwarder→丢弃
	}
	n := r.SendToPresenceIDs(ids, Message{Data: []byte("x")})
	if n != 1 {
		t.Fatalf("delivered=%d want 1 (remote dropped)", n)
	}
}

func TestRouter_CrossNode_NoLocalNode_AllLocal(t *testing.T) {
	// 未设 localNode:所有 id 视为本地(向后兼容)。
	var got atomic.Int32
	regs := &fakeRegistry{sinks: map[string]Sink{
		"s1": func(m Message) bool { got.Add(1); return true },
	}}
	fwd := &fakeForwarder{}
	r := New(regs, nil, WithForwarder(fwd)) // 无 localNode
	st := presence.Stream{Mode: 1, Subject: "r"}
	ids := []presence.ID{
		{SessionID: "s1", Stream: st, Node: "node-b"}, // Node 非空但 localNode=""→本地
	}
	n := r.SendToPresenceIDs(ids, Message{Data: []byte("x")})
	if n != 1 {
		t.Fatalf("delivered=%d want 1", n)
	}
	if fwd.count() != 0 {
		t.Fatalf("forward should not be called, got %d", fwd.count())
	}
}
