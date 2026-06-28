package inbox_test

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/rushteam/beauty/pkg/domain/inbox"
)

func TestInbox_Send_AssignsIDAndSeq(t *testing.T) {
	s := inbox.New(nil)
	m1 := s.Send(context.Background(), "alice", "bob", "chat", "hi")
	m2 := s.Send(context.Background(), "alice", "carol", "chat", "yo")
	if m1.ID == 0 || m2.ID <= m1.ID {
		t.Fatalf("ID should be monotonic: %d %d", m1.ID, m2.ID)
	}
	if m1.Seq != 1 || m2.Seq != 2 {
		t.Fatalf("seq want 1,2 got %d,%d", m1.Seq, m2.Seq)
	}
	if m1.OwnerID != "alice" || m1.FromID != "bob" {
		t.Fatalf("owner/from wrong: %+v", m1)
	}
}

func TestInbox_List_LatestFirst(t *testing.T) {
	s := inbox.New(nil)
	for i := range 5 {
		s.Send(context.Background(), "alice", "bob", "chat", "m"+string(rune('0'+i)))
	}
	list := s.List("alice", 0, 3)
	if len(list) != 3 {
		t.Fatalf("len want 3, got %d", len(list))
	}
	// 降序:最新(seq5)在前。
	if list[0].Seq != 5 || list[2].Seq != 3 {
		t.Fatalf("order want 5,4,3 got %d,%d,%d", list[0].Seq, list[1].Seq, list[2].Seq)
	}
}

func TestInbox_List_Pagination(t *testing.T) {
	s := inbox.New(nil)
	for range 10 {
		s.Send(context.Background(), "alice", "bob", "chat", "x")
	}
	// 第一页取最新3条。
	page1 := s.List("alice", 0, 3)
	if len(page1) != 3 || page1[2].Seq != 8 {
		t.Fatalf("page1 last seq want 8, got %d", page1[2].Seq)
	}
	// 第二页用 page1 最小 seq 作为 afterSeq。
	page2 := s.List("alice", page1[2].Seq, 3)
	if len(page2) != 3 || page2[0].Seq != 7 || page2[2].Seq != 5 {
		t.Fatalf("page2 want 7,6,5 got %d,%d,%d", page2[0].Seq, page2[1].Seq, page2[2].Seq)
	}
	page3 := s.List("alice", page2[2].Seq, 3)
	if len(page3) != 3 { // seq 4,3,2
		t.Fatalf("page3 len want 3, got %d", len(page3))
	}
	// 最后只剩 seq=1 一条。
	page4 := s.List("alice", page3[2].Seq, 3)
	if len(page4) != 1 || page4[0].Seq != 1 {
		t.Fatalf("page4 want [1], got %+v", page4)
	}
}

func TestInbox_List_Empty(t *testing.T) {
	s := inbox.New(nil)
	if list := s.List("nobody", 0, 10); list != nil {
		t.Fatalf("empty inbox want nil, got %v", list)
	}
}

func TestInbox_MarkRead_UpToSeq(t *testing.T) {
	s := inbox.New(nil)
	for range 5 {
		s.Send(context.Background(), "alice", "bob", "chat", "x")
	}
	if n := s.UnreadCount("alice"); n != 5 {
		t.Fatalf("unread want 5, got %d", n)
	}
	// 标记 seq<=3 已读。
	got := s.MarkRead("alice", 3)
	if got != 3 {
		t.Fatalf("marked want 3, got %d", got)
	}
	if n := s.UnreadCount("alice"); n != 2 {
		t.Fatalf("unread after mark want 2, got %d", n)
	}
	// 重复标记不增计。
	if got := s.MarkRead("alice", 3); got != 0 {
		t.Fatalf("re-mark want 0, got %d", got)
	}
}

func TestInbox_MarkOneRead(t *testing.T) {
	s := inbox.New(nil)
	s.Send(context.Background(), "alice", "bob", "chat", "x")
	if !s.MarkOneRead("alice", 1) {
		t.Fatal("mark one should find seq=1")
	}
	if s.UnreadCount("alice") != 0 {
		t.Fatal("unread should be 0")
	}
	if s.MarkOneRead("alice", 999) {
		t.Fatal("mark nonexistent seq should return false")
	}
}

func TestInbox_UnreadCount(t *testing.T) {
	s := inbox.New(nil)
	s.Send(context.Background(), "alice", "bob", "chat", "a")
	s.Send(context.Background(), "alice", "bob", "chat", "b")
	s.Send(context.Background(), "bob", "alice", "chat", "c")
	if n := s.UnreadCount("alice"); n != 2 {
		t.Fatalf("alice unread want 2, got %d", n)
	}
	if n := s.UnreadCount("bob"); n != 1 {
		t.Fatalf("bob unread want 1, got %d", n)
	}
}

func TestInbox_Delete(t *testing.T) {
	s := inbox.New(nil)
	s.Send(context.Background(), "alice", "bob", "chat", "a")
	s.Send(context.Background(), "alice", "bob", "chat", "b")
	if !s.Delete("alice", 1) {
		t.Fatal("delete seq=1 should succeed")
	}
	if s.Delete("alice", 1) {
		t.Fatal("re-delete should fail")
	}
	if s.Count("alice") != 1 {
		t.Fatalf("count after delete want 1, got %d", s.Count("alice"))
	}
}

func TestInbox_MaxPerBox_EvictsOldest(t *testing.T) {
	s := inbox.New(nil, inbox.WithMaxPerBox(3))
	for range 5 {
		s.Send(context.Background(), "alice", "bob", "chat", "x")
	}
	if s.Count("alice") != 3 {
		t.Fatalf("count want 3 (evicted), got %d", s.Count("alice"))
	}
	// 最旧2条被删,保留 seq 3,4,5。
	list := s.List("alice", 0, 10)
	if len(list) != 3 || list[2].Seq != 3 {
		t.Fatalf("want seq 5,4,3 got %+v", list)
	}
}

func TestInbox_SeqNotResetAfterEviction(t *testing.T) {
	s := inbox.New(nil, inbox.WithMaxPerBox(2))
	for range 4 {
		s.Send(context.Background(), "alice", "bob", "chat", "x")
	}
	// 即使驱逐了,seq 仍单调递增,新消息 seq=5。
	m := s.Send(context.Background(), "alice", "bob", "chat", "x")
	if m.Seq != 5 {
		t.Fatalf("seq after eviction want 5, got %d", m.Seq)
	}
}

func TestInbox_LiveSink_OnlineDeliver(t *testing.T) {
	var delivered atomic.Int32
	s := inbox.New(func(ownerID string, m *inbox.Message) bool {
		delivered.Add(1)
		return true
	})
	s.Send(context.Background(), "alice", "bob", "chat", "hi")
	if delivered.Load() != 1 {
		t.Fatal("live sink should be called on send")
	}
	if s.UnreadCount("alice") != 1 {
		t.Fatal("should still persist (inbox allows recall)")
	}
}

func TestInbox_PerUserIsolated(t *testing.T) {
	s := inbox.New(nil)
	s.Send(context.Background(), "alice", "bob", "chat", "a")
	s.Send(context.Background(), "bob", "alice", "chat", "b")
	if s.Count("alice") != 1 || s.Count("bob") != 1 {
		t.Fatal("inboxes should be isolated per user")
	}
}

func TestInbox_Concurrent(t *testing.T) {
	s := inbox.New(nil, inbox.WithMaxPerBox(1000))
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			s.Send(context.Background(), "alice", "bob", "chat", "x")
		})
	}
	wg.Wait()
	if s.Count("alice") != 50 {
		t.Fatalf("count want 50, got %d", s.Count("alice"))
	}
	// 并发读+写不 panic。
	var wg2 sync.WaitGroup
	for range 10 {
		wg2.Go(func() { s.List("alice", 0, 10) })
		wg2.Go(func() { s.UnreadCount("alice") })
	}
	wg2.Wait()
}

func TestInbox_MessageCopy_SafeToMutate(t *testing.T) {
	s := inbox.New(nil)
	s.Send(context.Background(), "alice", "bob", "chat", "orig")
	list := s.List("alice", 0, 1)
	list[0].Content = "tampered"
	// 内部存储不应被外部改动影响(List 返回的是拷贝)。
	again := s.List("alice", 0, 1)
	if !slices.Contains([]string{"orig"}, again[0].Content) {
		t.Fatalf("internal mutated: %q", again[0].Content)
	}
}
