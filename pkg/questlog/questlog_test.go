package questlog_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/rushteam/beauty/pkg/questlog"
)

func quests() []questlog.Quest[string] {
	return []questlog.Quest[string]{
		{ID: "kill10", Target: 10},
		{ID: "login3", Target: 3},
		{ID: "vip", Target: 1, Requires: []string{"kill10", "login3"}}, // 需前两个领完
	}
}

func TestProgressAndAchieve(t *testing.T) {
	l := questlog.New(quests())
	st, changed := l.Advance("u1", "kill10", 4)
	if !changed || st.Progress != 4 || st.Status != questlog.StatusInProgress {
		t.Fatalf("after +4: %+v changed=%v", st, changed)
	}
	st, _ = l.Advance("u1", "kill10", 100) // 超目标被夹到 10
	if st.Progress != 10 || st.Status != questlog.StatusAchieved {
		t.Fatalf("after overshoot: %+v", st)
	}
}

func TestClaim_OnlyWhenAchieved(t *testing.T) {
	l := questlog.New(quests())
	if l.Claim("u1", "kill10") {
		t.Fatal("claim before achieve should fail")
	}
	l.Advance("u1", "kill10", 10)
	if !l.Claim("u1", "kill10") {
		t.Fatal("claim after achieve should succeed")
	}
	if l.Claim("u1", "kill10") {
		t.Fatal("double claim should fail (idempotent)")
	}
	st, _ := l.StateOf("u1", "kill10")
	if st.Status != questlog.StatusClaimed {
		t.Fatalf("status = %v, want claimed", st.Status)
	}
}

func TestDependencyGating(t *testing.T) {
	l := questlog.New(quests())
	// vip 依赖 kill10 + login3 都领取,初始应 Locked。
	st, _ := l.StateOf("u1", "vip")
	if st.Status != questlog.StatusLocked {
		t.Fatalf("vip should be locked, got %v", st.Status)
	}
	// 推进 vip 无效(锁定)。
	if _, changed := l.Advance("u1", "vip", 1); changed {
		t.Fatal("advancing locked quest should not change")
	}
	// 完成并领取前置。
	l.Advance("u1", "kill10", 10)
	l.Advance("u1", "login3", 3)
	l.Claim("u1", "kill10")
	if st, _ := l.StateOf("u1", "vip"); st.Status != questlog.StatusLocked {
		t.Fatal("vip still locked until BOTH prereqs claimed")
	}
	l.Claim("u1", "login3")
	// 现在 vip 解锁,变 InProgress。
	if st, _ := l.StateOf("u1", "vip"); st.Status != questlog.StatusInProgress {
		t.Fatalf("vip should unlock after prereqs claimed, got %v", st.Status)
	}
	l.Advance("u1", "vip", 1)
	if !l.Claim("u1", "vip") {
		t.Fatal("vip should be claimable now")
	}
}

func TestClaimAll(t *testing.T) {
	l := questlog.New(quests())
	l.Advance("u1", "kill10", 10)
	l.Advance("u1", "login3", 3)
	got := l.ClaimAll("u1")
	if len(got) != 2 {
		t.Fatalf("ClaimAll = %v, want 2 (vip still locked)", got)
	}
}

func TestClaimable(t *testing.T) {
	l := questlog.New(quests())
	l.Advance("u1", "login3", 3)
	c := l.Claimable("u1")
	if len(c) != 1 || c[0] != "login3" {
		t.Fatalf("claimable = %v, want [login3]", c)
	}
}

func TestReset(t *testing.T) {
	l := questlog.New(quests())
	l.Advance("u1", "kill10", 10)
	l.Claim("u1", "kill10")
	l.Reset("u1", "kill10")
	st, _ := l.StateOf("u1", "kill10")
	if st.Progress != 0 || st.Status != questlog.StatusInProgress {
		t.Fatalf("after reset: %+v", st)
	}
}

func TestReset_All(t *testing.T) {
	l := questlog.New(quests())
	l.Advance("u1", "kill10", 5)
	l.Advance("u1", "login3", 2)
	l.Reset("u1")
	for _, st := range l.List("u1") {
		if st.ID != "vip" && st.Progress != 0 {
			t.Fatalf("%s not reset: %+v", st.ID, st)
		}
	}
}

func TestOwnerIsolation(t *testing.T) {
	l := questlog.New(quests())
	l.Advance("u1", "kill10", 10)
	if st, _ := l.StateOf("u2", "kill10"); st.Progress != 0 {
		t.Fatal("u2 progress should be independent of u1")
	}
}

func TestOnClaimCallback(t *testing.T) {
	var fired atomic.Int64
	l := questlog.New(quests(), questlog.WithOnClaim(func(owner string, q questlog.Quest[string]) {
		fired.Add(1)
	}))
	l.Advance("u1", "kill10", 10)
	l.Claim("u1", "kill10")
	if fired.Load() != 1 {
		t.Fatalf("onClaim fired %d times, want 1", fired.Load())
	}
}

func TestConcurrentAdvance(t *testing.T) {
	l := questlog.New([]questlog.Quest[string]{{ID: "big", Target: 100000}})
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			for range 1000 {
				l.Advance("u1", "big", 1)
			}
		})
	}
	wg.Wait()
	st, _ := l.StateOf("u1", "big")
	// 50*1000=50000 < target,进度应精确为 50000。
	if st.Progress != 50000 {
		t.Fatalf("concurrent progress = %d, want 50000", st.Progress)
	}
}
