package reddot_test

import (
	"sync"
	"testing"

	"github.com/rushteam/beauty/pkg/reddot"
)

func TestAggregation(t *testing.T) {
	tr := reddot.New()
	tr.Set("me/msg/chat", 3)
	tr.Set("me/msg/system", 2)
	tr.Set("me/friend/request", 5)

	// 叶子。
	if got := tr.Count("me/msg/chat"); got != 3 {
		t.Fatalf("chat = %d, want 3", got)
	}
	// 内部节点聚合。
	if got := tr.Count("me/msg"); got != 5 {
		t.Fatalf("me/msg = %d, want 5 (3+2)", got)
	}
	if got := tr.Count("me"); got != 10 {
		t.Fatalf("me = %d, want 10 (3+2+5)", got)
	}
	if got := tr.Total(); got != 10 {
		t.Fatalf("total = %d, want 10", got)
	}
}

func TestDot(t *testing.T) {
	tr := reddot.New()
	if tr.Dot("me") {
		t.Fatal("empty tree should have no dot")
	}
	tr.Set("me/msg/chat", 1)
	if !tr.Dot("me") || !tr.Dot("me/msg") || !tr.Dot("me/msg/chat") {
		t.Fatal("dot should propagate up to ancestors")
	}
	if tr.Dot("me/friend") {
		t.Fatal("unrelated branch should have no dot")
	}
}

func TestClearPropagatesUp(t *testing.T) {
	tr := reddot.New()
	tr.Set("me/msg/chat", 3)
	tr.Set("me/msg/system", 2)
	tr.Set("me/friend/request", 5)

	// 清 me/msg 子树 → me 只剩 friend 的 5。
	tr.Clear("me/msg")
	if got := tr.Count("me/msg"); got != 0 {
		t.Fatalf("after clear me/msg = %d, want 0", got)
	}
	if got := tr.Count("me"); got != 5 {
		t.Fatalf("me after partial clear = %d, want 5", got)
	}
}

func TestClearLeaf(t *testing.T) {
	tr := reddot.New()
	tr.Set("me/msg/chat", 3)
	tr.Set("me/msg/system", 2)
	tr.Clear("me/msg/chat") // 只清一个叶子
	if got := tr.Count("me/msg"); got != 2 {
		t.Fatalf("me/msg = %d, want 2", got)
	}
}

func TestIncr(t *testing.T) {
	tr := reddot.New()
	if got := tr.Incr("me/msg/chat", 1); got != 1 {
		t.Fatalf("incr = %d", got)
	}
	tr.Incr("me/msg/chat", 5)
	if got := tr.Count("me/msg/chat"); got != 6 {
		t.Fatalf("count = %d, want 6", got)
	}
	// 减到负数夹为 0。
	if got := tr.Incr("me/msg/chat", -100); got != 0 {
		t.Fatalf("incr clamp = %d, want 0", got)
	}
}

func TestSetNegativeClamped(t *testing.T) {
	tr := reddot.New()
	tr.Set("a", -5)
	if tr.Count("a") != 0 {
		t.Fatal("negative set should clamp to 0")
	}
}

func TestNonexistentPath(t *testing.T) {
	tr := reddot.New()
	if tr.Count("nope/nope") != 0 || tr.Dot("nope") {
		t.Fatal("nonexistent path should be 0/false")
	}
	if tr.Children("nope") != nil {
		t.Fatal("nonexistent children should be nil")
	}
}

func TestChildren(t *testing.T) {
	tr := reddot.New()
	tr.Set("me/msg/chat", 3)
	tr.Set("me/msg/system", 2)
	tr.Set("me/friend/request", 5)

	kids := tr.Children("me")
	if len(kids) != 2 {
		t.Fatalf("me children = %d, want 2", len(kids))
	}
	// 排序后 friend 在前、msg 在后。
	if kids[0].Name != "friend" || kids[0].Count != 5 {
		t.Fatalf("kids[0] = %+v", kids[0])
	}
	if kids[1].Name != "msg" || kids[1].Count != 5 {
		t.Fatalf("kids[1] = %+v", kids[1])
	}
}

func TestCustomSeparator(t *testing.T) {
	tr := reddot.New(reddot.WithSeparator("."))
	tr.Set("me.msg.chat", 4)
	if tr.Count("me.msg") != 4 {
		t.Fatal("custom separator aggregation failed")
	}
}

func TestConcurrent(t *testing.T) {
	tr := reddot.New()
	var wg sync.WaitGroup
	paths := []string{"me/a", "me/b", "me/c/x", "me/c/y"}
	for _, p := range paths {
		wg.Go(func() {
			for range 1000 {
				tr.Incr(p, 1)
			}
		})
	}
	// 并发读
	for range 10 {
		wg.Go(func() {
			for range 1000 {
				_ = tr.Total()
				_ = tr.Count("me")
			}
		})
	}
	wg.Wait()
	if got := tr.Total(); got != 4000 {
		t.Fatalf("total = %d, want 4000", got)
	}
}
