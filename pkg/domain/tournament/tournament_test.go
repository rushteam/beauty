package tournament_test

import (
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/domain/tournament"
	"github.com/rushteam/beauty/pkg/leaderboard"
)

func TestTournament_Basic(t *testing.T) {
	// 每分钟重置(测试用),降序。
	tm, err := tournament.New("daily", leaderboard.SortDescending, "* * * * *")
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	tm.Fill([]leaderboard.Record{
		{OwnerID: "a", Score: 100},
		{OwnerID: "b", Score: 300},
		{OwnerID: "c", Score: 200},
	}, true)
	if got := tm.Get("b"); got != 1 {
		t.Fatalf("b rank=%d, want 1", got)
	}
	top := tm.TopN(2)
	if len(top) != 2 || top[0].OwnerID != "b" || top[1].OwnerID != "c" {
		t.Fatalf("top2=%+v", top)
	}
	if tm.Size() != 3 {
		t.Fatalf("size=%d", tm.Size())
	}
}

func TestTournament_InsertAndDelete(t *testing.T) {
	tm, _ := tournament.New("wk", leaderboard.SortDescending, "0 0 * * *")
	tm.Fill([]leaderboard.Record{{OwnerID: "a", Score: 10}}, true)
	rank := tm.Insert(leaderboard.Record{OwnerID: "b", Score: 50}, true)
	if rank != 1 {
		t.Fatalf("b rank=%d, want 1", rank)
	}
	if got := tm.Get("a"); got != 2 {
		t.Fatalf("a rank=%d, want 2", got)
	}
	if !tm.Delete("b") {
		t.Fatal("delete failed")
	}
	if got := tm.Get("b"); got != -1 {
		t.Fatalf("after delete rank=%d, want -1", got)
	}
}

func TestTournament_InvalidCron(t *testing.T) {
	_, err := tournament.New("bad", leaderboard.SortDescending, "not a cron")
	if err == nil {
		t.Fatal("want error for invalid cron")
	}
}

func TestTournament_NextReset(t *testing.T) {
	// 每天 0 点重置。
	tm, _ := tournament.New("daily", leaderboard.SortDescending, "0 0 * * *")
	next := tm.NextReset()
	if next.Before(time.Now()) {
		t.Fatal("next reset should be in future")
	}
	// 下一个 0 点。
	if next.Hour() != 0 || next.Minute() != 0 {
		t.Fatalf("next reset not at 00:00: %v", next)
	}
}

func TestTournament_CurrentExpiry(t *testing.T) {
	tm, _ := tournament.New("m", leaderboard.SortDescending, "* * * * *")
	e1 := tm.CurrentExpiry()
	// expiry 应是下一个整分钟。
	tm2 := time.Unix(e1, 0)
	if tm2.Second() != 0 {
		t.Fatalf("expiry not on minute boundary: %v", tm2)
	}
	if !tm2.After(time.Now()) {
		t.Fatal("expiry should be in future")
	}
}

func TestTournament_SharedRankCache(t *testing.T) {
	rc := leaderboard.New()
	tm1, _ := tournament.New("t1", leaderboard.SortDescending, "* * * * *", tournament.WithRankCache(rc))
	tm2, _ := tournament.New("t2", leaderboard.SortDescending, "* * * * *", tournament.WithRankCache(rc))
	tm1.Fill([]leaderboard.Record{{OwnerID: "a", Score: 10}}, true)
	tm2.Fill([]leaderboard.Record{{OwnerID: "a", Score: 99}}, true)
	// 不同锦标赛 ID,即使共享 RankCache 也互不干扰。
	if got := tm1.Get("a"); got != 1 {
		t.Fatalf("t1 a=%d", got)
	}
	if got := tm2.Get("a"); got != 1 {
		t.Fatalf("t2 a=%d", got)
	}
}

func TestTournament_Around(t *testing.T) {
	tm, _ := tournament.New("a", leaderboard.SortDescending, "* * * * *")
	recs := make([]leaderboard.Record, 10)
	for i := range recs {
		recs[i] = leaderboard.Record{OwnerID: string(rune('a' + i)), Score: int64((i + 1) * 100)}
	}
	tm.Fill(recs, true)
	// 'e'(score 500)是第 6 名,around 2 应含 d/e/f(4,5,6 名?实际 5,6,7)。
	around := tm.Around("e", 2)
	if len(around) == 0 {
		t.Fatal("around empty")
	}
	found := false
	for _, r := range around {
		if r.OwnerID == "e" {
			found = true
		}
	}
	if !found {
		t.Fatal("around should include self")
	}
}
