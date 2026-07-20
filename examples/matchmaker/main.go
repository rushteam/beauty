// matchmaker 示例:按属性匹配组队。
//
// 演示 pkg/matchmaker:玩家带 region+skill 属性注册 ticket,匹配器按桶分组、
// skill 排序贪心凑队,凑齐即回调。HTTP 端点入队、查询候选数。
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/matchmaker"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

func main() {
	var matched atomic.Int32
	var mu sync.Mutex
	teams := make([][]string, 0)

	m := matchmaker.New(func(ctx context.Context, mm matchmaker.Match) error {
		mu.Lock()
		ids := make([]string, len(mm.Tickets))
		for i, t := range mm.Tickets {
			ids[i] = t.Presence.UserID
		}
		teams = append(teams, ids)
		mu.Unlock()
		matched.Add(1)
		println("matched team:", ids)
		return nil
	}, matchmaker.WithTickInterval(500*time.Millisecond), matchmaker.WithMaxWaitSec(15))
	m.Start(context.Background())

	mux := http.NewServeMux()

	// /queue?user=u1&region=eu&skill=1000  入队(2-3 人队)。
	mux.HandleFunc("/queue", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		skill, _ := strconv.ParseFloat(q.Get("skill"), 64)
		_, err := m.Add(matchmaker.Ticket{
			Presence: matchmaker.Presence{UserID: q.Get("user"), SessionID: q.Get("user")},
			Properties: matchmaker.Properties{
				String:  map[string]string{"region": q.Get("region")},
				Numeric: map[string]float64{"skill": skill},
			},
			MinCount: 2, MaxCount: 3,
		}, "5v5", q.Get("region")+"|ranked")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Write([]byte("queued"))
	})

	// /stats  查询候选数与已匹配数。
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		teamCount := len(teams)
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{
			"waiting": m.Count(),
			"matched": matched.Load(),
			"teams":   teamCount,
		})
	})

	app := beauty.New(beauty.WithWebServer(":8287", mux, webserver.WithServiceName("matchmaker-demo")))
	println("matchmaker demo on :8287")
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
