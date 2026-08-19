package presence

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestTracker_TrackUntrack(t *testing.T) {
	tr := New(nil, 16)
	st := Stream{Mode: 1, Subject: "room1"}

	_, added := tr.Track("s1", st, Meta{UserID: "u1", Username: "alice"})
	if !added {
		t.Fatal("expected newly tracked")
	}
	_, added = tr.Track("s1", st, Meta{UserID: "u1"})
	if added {
		t.Fatal("expected not newly tracked (idempotent)")
	}
	if tr.Count() != 1 {
		t.Fatalf("count=%d want 1", tr.Count())
	}

	members := tr.ListByStream(st, false)
	if len(members) != 1 || members[0].Meta.UserID != "u1" {
		t.Fatalf("members=%+v", members)
	}

	if !tr.Untrack("s1", st, "u1") {
		t.Fatal("Untrack returned false")
	}
	if tr.Count() != 0 {
		t.Fatalf("count after untrack=%d", tr.Count())
	}
	if tr.StreamExists(st) {
		t.Fatal("stream should not exist")
	}
}

func TestTracker_UntrackAll(t *testing.T) {
	tr := New(nil, 16)
	s1 := Stream{Mode: 1, Subject: "r1"}
	s2 := Stream{Mode: 1, Subject: "r2"}
	tr.Track("sess", s1, Meta{UserID: "u"})
	tr.Track("sess", s2, Meta{UserID: "u"})

	removed := tr.UntrackAll("sess")
	if len(removed) != 2 {
		t.Fatalf("removed=%d want 2", len(removed))
	}
	if tr.Count() != 0 {
		t.Fatalf("count=%d want 0", tr.Count())
	}
}

func TestTracker_HiddenNotListed(t *testing.T) {
	tr := New(nil, 16)
	st := Stream{Mode: 1, Subject: "r"}
	tr.Track("s1", st, Meta{UserID: "u1", Hidden: true})
	tr.Track("s2", st, Meta{UserID: "u2"})

	if len(tr.ListByStream(st, false)) != 1 {
		t.Fatal("hidden should be excluded without includeHidden")
	}
	if len(tr.ListByStream(st, true)) != 2 {
		t.Fatal("hidden should be included with includeHidden")
	}
}

func TestTracker_Events(t *testing.T) {
	var (
		mu     sync.Mutex
		joins  int
		leaves int
	)
	tr := New(func(stream Stream, j, l []*Presence) {
		mu.Lock()
		joins += len(j)
		leaves += len(l)
		mu.Unlock()
	}, 16)
	tr.Start(context.Background())
	defer func() {
		tr.Stop()
		tr.Wait()
	}()

	st := Stream{Mode: 1, Subject: "r"}
	tr.Track("s1", st, Meta{UserID: "u1"})
	tr.Untrack("s1", st, "u1")

	deadline := time.After(time.Second)
	for {
		mu.Lock()
		j, l := joins, leaves
		mu.Unlock()
		if j >= 1 && l >= 1 {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("events not delivered: joins=%d leaves=%d", j, l)
		default:
		}
		time.Sleep(time.Millisecond)
	}
}

func TestTracker_DualIndex(t *testing.T) {
	tr := New(nil, 16)
	s1 := Stream{Mode: 1, Subject: "r1"}
	s2 := Stream{Mode: 2, Subject: "r2"}
	tr.Track("sess", s1, Meta{UserID: "u"})
	tr.Track("sess", s2, Meta{UserID: "u"})

	// 按会话查:应在两个流里。
	if len(tr.ListBySession("sess")) != 2 {
		t.Fatal("ListBySession should return 2")
	}
	// 按流查:各 1 个。
	if tr.CountByStream(s1) != 1 || tr.CountByStream(s2) != 1 {
		t.Fatal("CountByStream mismatch")
	}
}

func TestTracker_Concurrent(t *testing.T) {
	tr := New(nil, 64)
	st := Stream{Mode: 1, Subject: "r"}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				tr.Track("s", st, Meta{UserID: "u"})
				tr.Untrack("s", st, "u")
			}
		}(i)
	}
	wg.Wait()
}
