// tournament 示例:每日重置排行榜。
//
// 演示 pkg/tournament:基于 cron 的周期性重置,薄层封装 pkg/leaderboard。
// /submit 提交分数(自动算当前周期 expiry),/rank 查名次,/top 取榜单。
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/leaderboard"
	"github.com/rushteam/beauty/pkg/service/webserver"
	"github.com/rushteam/beauty/pkg/domain/tournament"
)

func main() {
	// 每天 0 点重置的降序榜。
	tm, err := tournament.New("daily_score", leaderboard.SortDescending, "0 0 * * *",
		tournament.WithDuration(int64(24*time.Hour/time.Second)),
	)
	if err != nil {
		panic(err)
	}
	tm.Fill([]leaderboard.Record{
		{OwnerID: "alice", Score: 1500},
		{OwnerID: "bob", Score: 3000},
		{OwnerID: "carol", Score: 2000},
	}, true)

	mux := http.NewServeMux()

	// /rank?user=alice  查当前周期名次。
	mux.HandleFunc("/rank", func(w http.ResponseWriter, r *http.Request) {
		user := r.URL.Query().Get("user")
		json.NewEncoder(w).Encode(map[string]any{
			"user": user, "rank": tm.Get(user),
			"next_reset": tm.NextReset().Format(time.RFC3339),
		})
	})

	// /top?n=3  当前周期前 N 名。
	mux.HandleFunc("/top", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("n"))
		if n <= 0 {
			n = 10
		}
		json.NewEncoder(w).Encode(tm.TopN(n))
	})

	// /submit?user=dave&score=2500  提交分数。
	mux.HandleFunc("/submit", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		score, _ := strconv.ParseInt(q.Get("score"), 10, 64)
		rank := tm.Insert(leaderboard.Record{OwnerID: q.Get("user"), Score: score}, true)
		json.NewEncoder(w).Encode(map[string]any{"user": q.Get("user"), "new_rank": rank})
	})

	app := beauty.New(beauty.WithWebServer(":8291", mux, webserver.WithServiceName("tournament-demo")))
	println("tournament demo on :8291  (resets daily at 00:00)")
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
