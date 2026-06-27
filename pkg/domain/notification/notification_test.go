package notification_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/rushteam/beauty/pkg/domain/notification"
)

func TestStore_PersistentStored(t *testing.T) {
	var live int32
	s := notification.New(func(uid string, n *notification.Notification) bool {
		atomic.AddInt32(&live, 1)
		return true
	})
	n := s.Send(context.Background(), &notification.Notification{
		UserID: "u1", Subject: "hi", Persistent: true,
	})
	if n == nil || n.ID == 0 || n.Seq != 1 {
		t.Fatalf("stored n=%+v", n)
	}
	if atomic.LoadInt32(&live) != 1 {
		t.Fatal("live sink not called")
	}
	if s.Count("u1") != 1 {
		t.Fatal("count != 1")
	}
}

func TestStore_TransientNotStored(t *testing.T) {
	var live int32
	s := notification.New(func(uid string, n *notification.Notification) bool {
		atomic.AddInt32(&live, 1)
		return false // 不在线
	})
	n := s.Send(context.Background(), &notification.Notification{
		UserID: "u1", Subject: "ping", Persistent: false,
	})
	if n != nil {
		t.Fatal("transient should not return stored")
	}
	if atomic.LoadInt32(&live) != 1 {
		t.Fatal("live should still be attempted")
	}
	if s.Count("u1") != 0 {
		t.Fatal("transient should not be stored")
	}
}

func TestStore_ListPagination(t *testing.T) {
	s := notification.New(nil)
	for i := range 5 {
		s.Send(context.Background(), &notification.Notification{
			UserID: "u1", Subject: "m", Persistent: true, CreateTime: int64(i),
		})
	}
	// 第一页 limit=2。
	page1 := s.List("u1", 0, 2)
	if len(page1) != 2 || page1[0].Seq != 1 || page1[1].Seq != 2 {
		t.Fatalf("page1=%+v", page1)
	}
	// 续传:afterSeq=2。
	page2 := s.List("u1", 2, 2)
	if len(page2) != 2 || page2[0].Seq != 3 || page2[1].Seq != 4 {
		t.Fatalf("page2=%+v", page2)
	}
	page3 := s.List("u1", 4, 2)
	if len(page3) != 1 || page3[0].Seq != 5 {
		t.Fatalf("page3=%+v", page3)
	}
	// 不重复:afterSeq=4 再取一次,应只有 seq=5。
	page3b := s.List("u1", 4, 10)
	if len(page3b) != 1 {
		t.Fatalf("page3b=%+v", page3b)
	}
}

func TestStore_Delete(t *testing.T) {
	s := notification.New(nil)
	n1 := s.Send(context.Background(), &notification.Notification{UserID: "u1", Subject: "a", Persistent: true})
	s.Send(context.Background(), &notification.Notification{UserID: "u1", Subject: "b", Persistent: true})
	if s.Count("u1") != 2 {
		t.Fatal("want 2")
	}
	if !s.Delete("u1", n1.ID) {
		t.Fatal("delete failed")
	}
	if s.Count("u1") != 1 {
		t.Fatal("want 1 after delete")
	}
	if s.Delete("u1", 999) {
		t.Fatal("delete missing should fail")
	}
}

func TestStore_DeleteAll(t *testing.T) {
	s := notification.New(nil)
	s.Send(context.Background(), &notification.Notification{UserID: "u1", Subject: "a", Persistent: true})
	s.Send(context.Background(), &notification.Notification{UserID: "u2", Subject: "b", Persistent: true})
	s.DeleteAll("u1")
	if s.Count("u1") != 0 || s.Count("u2") != 1 {
		t.Fatal("deleteall failed")
	}
}

func TestStore_MaxPerUser(t *testing.T) {
	s := notification.New(nil, notification.WithMaxPerUser(3))
	for range 5 {
		s.Send(context.Background(), &notification.Notification{UserID: "u1", Subject: "x", Persistent: true})
	}
	if s.Count("u1") != 3 {
		t.Fatalf("want 3 (capped), got %d", s.Count("u1"))
	}
	list := s.List("u1", 0, 10)
	// 应保留最新的 3 条(seq 3,4,5)。
	if list[0].Seq != 3 || list[2].Seq != 5 {
		t.Fatalf("capped list=%+v", list)
	}
}

func TestStore_MultiUserIsolation(t *testing.T) {
	s := notification.New(nil)
	s.Send(context.Background(), &notification.Notification{UserID: "u1", Subject: "a", Persistent: true})
	s.Send(context.Background(), &notification.Notification{UserID: "u2", Subject: "b", Persistent: true})
	if s.List("u1", 0, 10)[0].Subject != "a" {
		t.Fatal("u1 leak")
	}
	if s.List("u2", 0, 10)[0].Subject != "b" {
		t.Fatal("u2 leak")
	}
}

func TestStore_Concurrent(t *testing.T) {
	s := notification.New(nil)
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Send(context.Background(), &notification.Notification{UserID: "u1", Subject: "c", Persistent: true})
		}()
	}
	wg.Wait()
	if s.Count("u1") != 50 {
		t.Fatalf("count=%d, want 50", s.Count("u1"))
	}
	list := s.List("u1", 0, 100)
	// seq 应全局有序(1..50)。
	for i, n := range list {
		if n.Seq != int64(i+1) {
			t.Fatalf("seq[%d]=%d", i, n.Seq)
		}
	}
}
