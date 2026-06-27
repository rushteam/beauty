package leaderboard

import (
	"testing"
)

func TestRankCache_FillAndGet(t *testing.T) {
	rc := New()
	rc.Fill("lb1", 1000, SortDescending, []Record{
		{OwnerID: "a", Score: 100},
		{OwnerID: "b", Score: 300},
		{OwnerID: "c", Score: 200},
	}, true)

	if got := rc.Get("lb1", 1000, "b"); got != 1 {
		t.Fatalf("b rank=%d want 1", got)
	}
	if got := rc.Get("lb1", 1000, "c"); got != 2 {
		t.Fatalf("c rank=%d want 2", got)
	}
	if got := rc.Get("lb1", 1000, "a"); got != 3 {
		t.Fatalf("a rank=%d want 3", got)
	}
}

func TestRankCache_Insert(t *testing.T) {
	rc := New()
	rc.Fill("lb", 1, SortDescending, []Record{
		{OwnerID: "a", Score: 100},
	}, true)
	// 插入更高分,应排第 1。
	rank := rc.Insert("lb", 1, SortDescending, Record{OwnerID: "b", Score: 200}, true)
	if rank != 1 {
		t.Fatalf("b rank=%d want 1", rank)
	}
	if got := rc.Get("lb", 1, "a"); got != 2 {
		t.Fatalf("a rank after insert=%d want 2", got)
	}
}

func TestRankCache_Delete(t *testing.T) {
	rc := New()
	rc.Fill("lb", 1, SortDescending, []Record{
		{OwnerID: "a", Score: 100},
		{OwnerID: "b", Score: 200},
	}, true)
	if !rc.Delete("lb", 1, "b") {
		t.Fatal("Delete returned false")
	}
	if got := rc.Get("lb", 1, "b"); got != -1 {
		t.Fatalf("deleted rank=%d want -1", got)
	}
	if got := rc.Get("lb", 1, "a"); got != 1 {
		t.Fatalf("a rank after delete=%d want 1", got)
	}
}

func TestRankCache_Blacklist(t *testing.T) {
	rc := New("lb_secret")
	n := rc.Fill("lb_secret", 1, SortDescending, []Record{{OwnerID: "a", Score: 1}}, true)
	if n != 0 {
		t.Fatalf("blacklisted fill should return 0, got %d", n)
	}
	if got := rc.Get("lb_secret", 1, "a"); got != -1 {
		t.Fatalf("blacklisted Get should return -1, got %d", got)
	}
}

func TestRankCache_TopN(t *testing.T) {
	rc := New()
	rc.Fill("lb", 1, SortDescending, []Record{
		{OwnerID: "a", Score: 100},
		{OwnerID: "b", Score: 300},
		{OwnerID: "c", Score: 200},
	}, true)
	top := rc.TopN("lb", 1, 2)
	if len(top) != 2 || top[0].OwnerID != "b" || top[1].OwnerID != "c" {
		t.Fatalf("TopN=%+v", top)
	}
}

func TestRankCache_Around(t *testing.T) {
	rc := New()
	rc.Fill("lb", 1, SortDescending, []Record{
		{OwnerID: "a", Score: 100},
		{OwnerID: "b", Score: 200},
		{OwnerID: "c", Score: 300},
		{OwnerID: "d", Score: 400},
		{OwnerID: "e", Score: 500},
	}, true)
	around := rc.Around("lb", 1, "c", 1)
	if len(around) != 3 {
		t.Fatalf("around len=%d want 3", len(around))
	}
}

func TestRankCache_Ascending(t *testing.T) {
	rc := New()
	rc.Fill("lb", 1, SortAscending, []Record{
		{OwnerID: "a", Score: 100},
		{OwnerID: "b", Score: 50},
		{OwnerID: "c", Score: 200},
	}, true)
	if got := rc.Get("lb", 1, "b"); got != 1 {
		t.Fatalf("ascending: b rank=%d want 1", got)
	}
}
