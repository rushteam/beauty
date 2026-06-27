package relationship_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/rushteam/beauty/pkg/domain/relationship"
)

func TestGraph_AddEdge(t *testing.T) {
	g := relationship.New()
	if err := g.AddEdge(relationship.Edge{Source: "a", Destination: "b", State: relationship.StateActive, Position: 1}); err != nil {
		t.Fatalf("add: %v", err)
	}
	e, err := g.Edge("a", "b")
	if err != nil || e.State != relationship.StateActive {
		t.Fatalf("edge: %v %v", e, err)
	}
	if err := g.AddEdge(relationship.Edge{Source: "a", Destination: "b", Position: 2}); !errors.Is(err, relationship.ErrAlreadyExists) {
		t.Fatalf("want ErrAlreadyExists, got %v", err)
	}
}

func TestGraph_RemoveEdge(t *testing.T) {
	g := relationship.New()
	g.AddEdge(relationship.Edge{Source: "a", Destination: "b", Position: 1})
	if err := g.RemoveEdge("a", "b"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := g.Edge("a", "b"); !errors.Is(err, relationship.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if err := g.RemoveEdge("a", "b"); !errors.Is(err, relationship.ErrNotFound) {
		t.Fatalf("remove again want ErrNotFound, got %v", err)
	}
}

func TestGraph_Friends(t *testing.T) {
	g := relationship.New()
	if err := g.AddFriend("a", "b", 1); err != nil {
		t.Fatalf("addfriend: %v", err)
	}
	friends := g.Friends("a")
	if len(friends) != 1 || friends[0] != "b" {
		t.Fatalf("friends=%v", friends)
	}
	// b 视角也是 a 的好友。
	friendsB := g.Friends("b")
	if len(friendsB) != 1 || friendsB[0] != "a" {
		t.Fatalf("friendsB=%v", friendsB)
	}
	// 删除好友:双向都删。
	g.RemoveFriend("a", "b")
	if g.Friends("a") != nil || g.Friends("b") != nil {
		t.Fatal("friends should be empty after remove")
	}
}

func TestGraph_AddFriendBlocked(t *testing.T) {
	g := relationship.New()
	g.Block("b", "a", 1) // b 拉黑 a
	if err := g.AddFriend("a", "b", 2); !errors.Is(err, relationship.ErrBlocked) {
		t.Fatalf("want ErrBlocked, got %v", err)
	}
	// 没有建立任何边。
	if friends := g.Friends("a"); friends != nil {
		t.Fatalf("no friend edge should exist, got %v", friends)
	}
}

func TestGraph_BlockRemovesExistingEdge(t *testing.T) {
	g := relationship.New()
	g.AddFriend("a", "b", 1)
	// a 拉黑 b:删除 a→b 的好友边,换成 block。
	g.Block("a", "b", 2)
	e, _ := g.Edge("a", "b")
	if e.State != relationship.StateBlocked {
		t.Fatalf("state=%d, want blocked", e.State)
	}
	// b→a 仍是好友边(block 是单向)。
	e2, _ := g.Edge("b", "a")
	if e2.State != relationship.StateActive {
		t.Fatalf("b->a state=%d, want active", e2.State)
	}
}

func TestGraph_IsBlocked(t *testing.T) {
	g := relationship.New()
	g.Block("a", "b", 1)
	if !g.IsBlocked("a", "b") {
		t.Fatal("a should block b")
	}
	if g.IsBlocked("b", "a") {
		t.Fatal("b should not block a (one-way)")
	}
}

func TestGraph_OutgoingPagination(t *testing.T) {
	g := relationship.New()
	// a 关注 10 个人,position 1..10。
	for i := 1; i <= 10; i++ {
		g.AddEdge(relationship.Edge{Source: "a", Destination: "u" + itoa(i), State: relationship.StateActive, Position: int64(i)})
	}
	// 第一页:limit 4,降序(10,9,8,7)。
	page1 := g.Outgoing("a", 0, 4, -1)
	if len(page1) != 4 || page1[0].Destination != "u10" || page1[3].Destination != "u7" {
		t.Fatalf("page1=%+v", page1)
	}
	// 续传:afterPosition=7,取 <7 的(6,5,4,3)。
	page2 := g.Outgoing("a", 7, 4, -1)
	if len(page2) != 4 || page2[0].Destination != "u6" || page2[3].Destination != "u3" {
		t.Fatalf("page2=%+v", page2)
	}
	// 最后一页:afterPosition=3,<3 的(2,1)。
	page3 := g.Outgoing("a", 3, 4, -1)
	if len(page3) != 2 || page3[0].Destination != "u2" || page3[1].Destination != "u1" {
		t.Fatalf("page3=%+v", page3)
	}
}

func TestGraph_OutgoingStateFilter(t *testing.T) {
	g := relationship.New()
	g.AddEdge(relationship.Edge{Source: "a", Destination: "f1", State: relationship.StateActive, Position: 1})
	g.AddEdge(relationship.Edge{Source: "a", Destination: "f2", State: relationship.StatePending, Position: 2})
	g.AddEdge(relationship.Edge{Source: "a", Destination: "f3", State: relationship.StateActive, Position: 3})
	active := g.Outgoing("a", 0, 10, relationship.StateActive)
	if len(active) != 2 {
		t.Fatalf("active=%d, want 2", len(active))
	}
	pending := g.Outgoing("a", 0, 10, relationship.StatePending)
	if len(pending) != 1 {
		t.Fatalf("pending=%d, want 1", len(pending))
	}
}

func TestGraph_Count(t *testing.T) {
	g := relationship.New()
	g.AddEdge(relationship.Edge{Source: "a", Destination: "b", State: relationship.StateActive, Position: 1})
	g.AddEdge(relationship.Edge{Source: "a", Destination: "c", State: relationship.StatePending, Position: 2})
	g.Block("a", "d", 3)
	if g.Count("a", -1) != 3 {
		t.Fatalf("total=%d, want 3", g.Count("a", -1))
	}
	if g.Count("a", relationship.StateActive) != 1 {
		t.Fatalf("active=%d, want 1", g.Count("a", relationship.StateActive))
	}
	if g.Count("a", relationship.StateBlocked) != 1 {
		t.Fatalf("blocked=%d, want 1", g.Count("a", relationship.StateBlocked))
	}
}

func TestGraph_MetadataPreserved(t *testing.T) {
	g := relationship.New()
	g.AddEdge(relationship.Edge{Source: "a", Destination: "b", State: relationship.StateActive, Position: 1, Metadata: map[string]string{"role": "admin"}})
	e, _ := g.Edge("a", "b")
	if e.Metadata["role"] != "admin" {
		t.Fatalf("metadata lost: %v", e.Metadata)
	}
}

func TestGraph_Concurrent(t *testing.T) {
	g := relationship.New()
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			g.AddEdge(relationship.Edge{Source: "a", Destination: "u" + itoa(i), State: relationship.StateActive, Position: int64(i)})
		}(i)
	}
	wg.Wait()
	if g.Count("a", -1) != 50 {
		t.Fatalf("count=%d, want 50", g.Count("a", -1))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
