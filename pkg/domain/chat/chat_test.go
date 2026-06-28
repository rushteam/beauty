package chat_test

import (
	"slices"
	"testing"

	"github.com/rushteam/beauty/pkg/domain/chat"
)

func TestChat_PostAndLatest(t *testing.T) {
	s := chat.New()
	m1 := s.Post("room1", "alice", "hi", 1)
	m2 := s.Post("room1", "bob", "yo", 2)
	if m1.MsgID != 1 || m2.MsgID != 2 {
		t.Fatalf("msgID: want 1,2 got %d,%d", m1.MsgID, m2.MsgID)
	}
	if m1.ID == m2.ID {
		t.Fatal("global ID should differ")
	}
	latest := s.Latest("room1", 10)
	if len(latest) != 2 {
		t.Fatalf("latest: want 2, got %d", len(latest))
	}
	// 降序:最新在前。
	if latest[0].MsgID != 2 || latest[1].MsgID != 1 {
		t.Fatalf("order: %+v", latest)
	}
}

func TestChat_Before_Pagination(t *testing.T) {
	s := chat.New()
	for i := range 10 {
		s.Post("c1", "u", "msg", int64(i))
	}
	// 取 msgID < 8 的最新 3 条 → 7,6,5(降序)。
	page := s.Before("c1", 8, 3)
	if len(page) != 3 {
		t.Fatalf("want 3, got %d", len(page))
	}
	if page[0].MsgID != 7 || page[1].MsgID != 6 || page[2].MsgID != 5 {
		t.Fatalf("page: %+v", page)
	}
	// 用最后一条的 MsgID 续传往前翻。
	page2 := s.Before("c1", page[2].MsgID, 3)
	if page2[0].MsgID != 4 || page2[2].MsgID != 2 {
		t.Fatalf("page2: %+v", page2)
	}
}

func TestChat_After_Incremental(t *testing.T) {
	s := chat.New()
	for i := range 5 {
		s.Post("c1", "u", "m", int64(i))
	}
	// 客户端已有 msgID=2,拉取之后的新消息 → 3,4,5(升序)。
	got := s.After("c1", 2, 10)
	if len(got) != 3 {
		t.Fatalf("want 3, got %d", len(got))
	}
	if got[0].MsgID != 3 || got[2].MsgID != 5 {
		t.Fatalf("order: %+v", got)
	}
}

func TestChat_MaxPerChannel_EvictsOldest(t *testing.T) {
	s := chat.New(chat.WithMaxPerChannel(3))
	for i := range 5 {
		s.Post("c1", "u", "m", int64(i))
	}
	if s.Count("c1") != 3 {
		t.Fatalf("want 3 after eviction, got %d", s.Count("c1"))
	}
	// 应保留 msgID 3,4,5。
	latest := s.Latest("c1", 10)
	ids := []int64{latest[0].MsgID, latest[1].MsgID, latest[2].MsgID}
	if !slices.Equal(ids, []int64{5, 4, 3}) {
		t.Fatalf("evicted wrong: %+v", ids)
	}
}

func TestChat_MsgID_MonotonicAfterEviction(t *testing.T) {
	s := chat.New(chat.WithMaxPerChannel(2))
	s.Post("c1", "u", "a", 1)
	s.Post("c1", "u", "b", 2)
	s.Post("c1", "u", "c", 3) // 淘汰 a(msgID=1)
	m4 := s.Post("c1", "u", "d", 4)
	if m4.MsgID != 4 {
		t.Fatalf("msgID should stay monotonic: got %d", m4.MsgID)
	}
	if s.LastMsgID("c1") != 4 {
		t.Fatalf("lastMsgID: got %d", s.LastMsgID("c1"))
	}
}

func TestChat_LastMsgID_Empty(t *testing.T) {
	s := chat.New()
	if s.LastMsgID("nope") != 0 {
		t.Fatal("empty channel lastMsgID should be 0")
	}
}

func TestChat_Delete(t *testing.T) {
	s := chat.New()
	m := s.Post("c1", "u", "x", 1)
	if !s.Delete("c1", m.ID) {
		t.Fatal("delete should succeed")
	}
	if s.Count("c1") != 0 {
		t.Fatal("count should be 0")
	}
	if s.Delete("c1", m.ID) {
		t.Fatal("double delete should fail")
	}
}

func TestChat_Post_EmptyChannelOrUser(t *testing.T) {
	s := chat.New()
	if s.Post("", "u", "x", 1) != nil {
		t.Fatal("empty channel should return nil")
	}
	if s.Post("c", "", "x", 1) != nil {
		t.Fatal("empty user should return nil")
	}
}

func TestChat_Before_EmptyChannel(t *testing.T) {
	s := chat.New()
	if got := s.Before("nope", 0, 10); got != nil {
		t.Fatalf("empty channel: want nil, got %+v", got)
	}
}

func TestChat_DifferentChannels_Independent(t *testing.T) {
	s := chat.New()
	s.Post("c1", "u", "a", 1)
	s.Post("c2", "u", "b", 2)
	if s.LastMsgID("c1") != 1 || s.LastMsgID("c2") != 1 {
		t.Fatal("channels should have independent msgID sequences")
	}
}
