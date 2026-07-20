package status_test

import (
	"sync"
	"testing"

	"github.com/rushteam/beauty/pkg/presence"
	"github.com/rushteam/beauty/pkg/presence/status"
)

// fakeStore 记录每个 sessionID 的在场流(用于 ListBySession)。
type fakeStore struct {
	mu     sync.Mutex
	bySess map[string][]*presence.Presence
}

func (s *fakeStore) ListBySession(sid string) []*presence.Presence {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bySess[sid]
}

// fakeFinder 返回预设的 watchers。
type fakeFinder struct {
	watchers map[string][]string // userID → 关注者列表
}

func (f fakeFinder) Watchers(userID string, _ int) []string {
	return f.watchers[userID]
}

// fakeNotifier 记录投递。
type fakeNotifier struct {
	mu       sync.Mutex
	delivers []deliver
}

type deliver struct {
	sids    []string
	payload []byte
}

func (n *fakeNotifier) Send(sids []string, payload []byte) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.delivers = append(n.delivers, deliver{sids: sids, payload: payload})
	return len(sids)
}

func (n *fakeNotifier) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.delivers)
}

func newDispatcher(t *testing.T) (*status.Dispatcher, *fakeNotifier) {
	t.Helper()
	store := &fakeStore{bySess: make(map[string][]*presence.Presence)}
	finder := fakeFinder{watchers: map[string][]string{
		"alice": {"bob", "carol"},
		"bob":   {"alice"},
	}}
	note := &fakeNotifier{}
	d := status.New(
		status.WithPresenceStore(store),
		status.WithWatcherFinder(finder),
		status.WithNotifier(note.Send),
	)
	return d, note
}

func TestStatus_OnlineOnFirstJoin(t *testing.T) {
	d, note := newDispatcher(t)
	// alice 首次加入流 → online,通知 bob + carol。
	d.OnPresence(
		presence.Stream{Mode: 1, Subject: "room1"},
		[]*presence.Presence{{ID: presence.ID{SessionID: "s1"}, Meta: presence.Meta{UserID: "alice"}}},
		nil,
	)
	if note.count() != 1 {
		t.Fatalf("want 1 deliver, got %d", note.count())
	}
	dlv := note.delivers[0]
	if len(dlv.sids) != 2 {
		t.Fatalf("want 2 watchers, got %d", len(dlv.sids))
	}
	if !contains(dlv.payload, "alice") || !contains(dlv.payload, "online") {
		t.Fatalf("payload: %s", dlv.payload)
	}
}

func TestStatus_NoDuplicateOnlineOnSecondStream(t *testing.T) {
	d, note := newDispatcher(t)
	st1 := presence.Stream{Mode: 1, Subject: "room1"}
	st2 := presence.Stream{Mode: 2, Subject: "party"}
	// alice 加入第一个流 → online 通知。
	d.OnPresence(st1,
		[]*presence.Presence{{Meta: presence.Meta{UserID: "alice"}}}, nil)
	// alice 加入第二个流 → 不应再触发 online(已在线)。
	d.OnPresence(st2,
		[]*presence.Presence{{Meta: presence.Meta{UserID: "alice"}}}, nil)
	if note.count() != 1 {
		t.Fatalf("want 1 deliver (no dup), got %d", note.count())
	}
}

func TestStatus_OfflineWhenAllStreamsLeft(t *testing.T) {
	d, note := newDispatcher(t)
	st1 := presence.Stream{Mode: 1, Subject: "room1"}
	st2 := presence.Stream{Mode: 2, Subject: "party"}
	// alice 加入两个流。
	d.OnPresence(st1,
		[]*presence.Presence{{Meta: presence.Meta{UserID: "alice"}}}, nil)
	d.OnPresence(st2,
		[]*presence.Presence{{Meta: presence.Meta{UserID: "alice"}}}, nil)
	// 离开第一个 → 仍在线(还有第二个)。
	d.OnPresence(st1, nil,
		[]*presence.Presence{{Meta: presence.Meta{UserID: "alice"}}})
	if note.count() != 1 {
		t.Fatalf("leaving 1 of 2 should not trigger offline, got %d", note.count())
	}
	// 离开第二个 → offline 通知。
	d.OnPresence(st2, nil,
		[]*presence.Presence{{Meta: presence.Meta{UserID: "alice"}}})
	if note.count() != 2 {
		t.Fatalf("leaving all should trigger offline, got %d", note.count())
	}
	dlv := note.delivers[1]
	if !contains(dlv.payload, "offline") {
		t.Fatalf("want offline, got %s", dlv.payload)
	}
}

func TestStatus_NoWatchers_NoDeliver(t *testing.T) {
	store := &fakeStore{bySess: make(map[string][]*presence.Presence)}
	finder := fakeFinder{watchers: map[string][]string{}} // 无人关注
	note := &fakeNotifier{}
	d := status.New(
		status.WithPresenceStore(store),
		status.WithWatcherFinder(finder),
		status.WithNotifier(note.Send),
	)
	d.OnPresence(presence.Stream{Mode: 1, Subject: "r"},
		[]*presence.Presence{{Meta: presence.Meta{UserID: "nobody"}}}, nil)
	if note.count() != 0 {
		t.Fatal("no watchers → no deliver")
	}
}

func TestStatus_Dispatch_Manual(t *testing.T) {
	d, note := newDispatcher(t)
	// 手动触发 offline 通知。
	d.Dispatch("alice", status.StateOffline, nil)
	if note.count() != 1 {
		t.Fatalf("want 1, got %d", note.count())
	}
}

func TestStatus_MultipleFinders_Dedup(t *testing.T) {
	store := &fakeStore{bySess: make(map[string][]*presence.Presence)}
	// 两个图谱都有 bob 关注 alice,应去重。
	f1 := fakeFinder{watchers: map[string][]string{"alice": {"bob", "carol"}}}
	f2 := fakeFinder{watchers: map[string][]string{"alice": {"bob", "dave"}}}
	note := &fakeNotifier{}
	d := status.New(
		status.WithPresenceStore(store),
		status.WithWatcherFinder(f1),
		status.WithWatcherFinder(f2),
		status.WithNotifier(note.Send),
	)
	d.OnPresence(presence.Stream{Mode: 1, Subject: "r"},
		[]*presence.Presence{{Meta: presence.Meta{UserID: "alice"}}}, nil)
	if note.count() != 1 {
		t.Fatalf("want 1, got %d", note.count())
	}
	if len(note.delivers[0].sids) != 3 { // bob, carol, dave
		t.Fatalf("want 3 unique watchers, got %d", len(note.delivers[0].sids))
	}
}

func TestStatus_StateString(t *testing.T) {
	if status.StateOnline.String() != "online" {
		t.Fatal("online string")
	}
	if status.StateOffline.String() != "offline" {
		t.Fatal("offline string")
	}
}

func TestStatus_SkipsEmptyUserID(t *testing.T) {
	d, note := newDispatcher(t)
	d.OnPresence(presence.Stream{Mode: 1, Subject: "r"},
		[]*presence.Presence{{Meta: presence.Meta{UserID: ""}}}, nil)
	if note.count() != 0 {
		t.Fatal("empty userID should be skipped")
	}
}

func contains(b []byte, s string) bool {
	for i := 0; i+len(s) <= len(b); i++ {
		if string(b[i:i+len(s)]) == s {
			return true
		}
	}
	return false
}
