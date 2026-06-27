// leaderboard 示例:排行榜内存缓存 + "我的名次"查询。
//
// 演示 pkg/leaderboard:Fill 全量加载、Insert 增量更新、Get 查名次、TopN 取榜单。
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/leaderboard"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

func main() {
	rc := leaderboard.New()

	// 预置一个榜。
	rc.Fill("score", 0, leaderboard.SortDescending, []leaderboard.Record{
		{OwnerID: "alice", Score: 1500},
		{OwnerID: "bob", Score: 3000},
		{OwnerID: "carol", Score: 2000},
	}, true)

	mux := http.NewServeMux()

	// /rank?user=alice  查我的名次。
	mux.HandleFunc("/rank", func(w http.ResponseWriter, r *http.Request) {
		user := r.URL.Query().Get("user")
		rank := rc.Get("score", 0, user)
		json.NewEncoder(w).Encode(map[string]any{"user": user, "rank": rank})
	})

	// /top?n=3  取前 N 名。
	mux.HandleFunc("/top", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("n"))
		if n <= 0 {
			n = 10
		}
		top := rc.TopN("score", 0, n)
		json.NewEncoder(w).Encode(top)
	})

	// /submit?user=dave&score=2500  提交分数(增量更新)。
	mux.HandleFunc("/submit", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		score, _ := strconv.ParseInt(q.Get("score"), 10, 64)
		rank := rc.Insert("score", 0, leaderboard.SortDescending,
			leaderboard.Record{OwnerID: q.Get("user"), Score: score}, true)
		json.NewEncoder(w).Encode(map[string]any{"user": q.Get("user"), "new_rank": rank})
	})

	app := beauty.New(beauty.WithWebServer(":8285", mux, webserver.WithServiceName("leaderboard-demo")))
	println("leaderboard demo on :8285")
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
