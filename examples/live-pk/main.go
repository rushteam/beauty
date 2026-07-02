// live-pk 组合 demo:直播 PK 后端,展示多个原语如何协作。
//
// 组合的原语:
//   - pkg/versus       —— PK 房间:双方倒计时对抗计分、到点定胜负、事件流
//   - pkg/idempotency  —— 送礼幂等:同一 giftID 重复请求只计一次(防网络重试重复扣礼物)
//   - pkg/counter      —— 送礼配额:每用户每分钟送礼次数上限(防刷)
//   - pkg/tally        —— 高频人气聚合:点赞海量 +1 内存合并、批量落地
//   - pkg/idgen        —— 生成 PK 房间 ID
//
// 运行:go run ./examples/live-pk  然后:
//
//	curl localhost:8310/start                  # 开一局 PK(A vs B,20s)
//	curl -N localhost:8310/watch               # SSE 订阅实时比分(另开终端)
//	curl "localhost:8310/gift?user=u1&side=A&val=100&gift=g1"   # 送礼加分
//	curl localhost:8310/like                   # 点赞(高频聚合)
//	curl localhost:8310/snapshot               # 当前比分快照
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/rushteam/beauty/pkg/counter"
	"github.com/rushteam/beauty/pkg/idempotency"
	"github.com/rushteam/beauty/pkg/idgen"
	"github.com/rushteam/beauty/pkg/tally"
	"github.com/rushteam/beauty/pkg/versus"
)

type server struct {
	ids       *idgen.Generator
	giftDedup *idempotency.Store[int64]
	quota     *counter.Counter
	likes     *tally.Tally[int64]

	mu    sync.Mutex
	match *versus.Match // 当前进行中的 PK(简化:同时只一局)
}

const (
	giftQuotaPerMin = 10 // 每用户每分钟最多送礼 10 次
	pkDuration      = 20 * time.Second
)

func main() {
	gen, _ := idgen.New(1)
	s := &server{
		ids:       gen,
		giftDedup: idempotency.New[int64](idempotency.WithTTL(10 * time.Minute)),
		quota:     counter.New(time.Minute),
		likes: tally.New(func(ctx context.Context, batch map[string]int64) {
			for room, n := range batch {
				fmt.Printf("[tally] 房间 %s 点赞 +%d 批量落地\n", room, n)
			}
		}, tally.WithFlushInterval(2*time.Second)),
	}
	defer s.giftDedup.Stop()
	defer s.quota.Stop()
	defer s.likes.Stop()

	mux := http.NewServeMux()
	mux.HandleFunc("/start", s.handleStart)
	mux.HandleFunc("/gift", s.handleGift)
	mux.HandleFunc("/like", s.handleLike)
	mux.HandleFunc("/snapshot", s.handleSnapshot)
	mux.HandleFunc("/watch", s.handleWatch)

	fmt.Println("live-pk demo 监听 :8310")
	fmt.Println("  /start  /gift?user=&side=&val=&gift=  /like  /snapshot  /watch(SSE)")
	http.ListenAndServe(":8310", mux)
}

// /start 开一局 PK。
func (s *server) handleStart(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.match != nil && s.match.Snapshot().Running {
		http.Error(w, "已有进行中的 PK", http.StatusConflict)
		return
	}
	id := fmt.Sprintf("pk-%d", s.ids.MustNext())
	m := versus.New(id, []string{"A", "B"},
		versus.WithDuration(pkDuration),
		versus.WithOnEnd(func(res versus.Result) {
			fmt.Printf("[versus] PK %s 结束: 比分=%v winner=%q tie=%v\n", res.ID, res.Scores, res.Winner, res.Tie)
		}))
	s.match = m
	_ = m.Start()
	writeJSON(w, map[string]any{"pk_id": id, "duration": pkDuration.String()})
}

// /gift 送礼加分:幂等去重 + 配额 + 计分。
func (s *server) handleGift(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	user, side, giftID := q.Get("user"), q.Get("side"), q.Get("gift")
	val := parseInt(q.Get("val"), 1)
	if user == "" || (side != "A" && side != "B") || giftID == "" {
		http.Error(w, "需要 user, side(A|B), gift, val", http.StatusBadRequest)
		return
	}

	m := s.currentMatch()
	if m == nil {
		http.Error(w, "没有进行中的 PK", http.StatusNotFound)
		return
	}

	// 1) 配额:每用户每分钟送礼次数上限(防刷)。
	if !s.quota.Allow("gift:"+user, 1, giftQuotaPerMin) {
		http.Error(w, "送礼太频繁,请稍后", http.StatusTooManyRequests)
		return
	}

	// 2) 幂等:同一 giftID 重复请求只计一次(网络重试安全)。返回该次加分后的比分。
	score, err, shared := s.giftDedup.Do("gift:"+giftID, func() (int64, error) {
		return m.Add(side, val)
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, map[string]any{
		"side": side, "side_score": score,
		"replayed": shared, // true=重复请求,未重复加分
	})
}

// /like 点赞:高频,只做聚合计数(不逐笔落地)。
func (s *server) handleLike(w http.ResponseWriter, r *http.Request) {
	m := s.currentMatch()
	room := "none"
	if m != nil {
		room = m.Snapshot().ID
	}
	s.likes.Add(room, 1)
	w.WriteHeader(http.StatusNoContent)
}

// /snapshot 当前比分快照。
func (s *server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	m := s.currentMatch()
	if m == nil {
		http.Error(w, "没有进行中的 PK", http.StatusNotFound)
		return
	}
	writeJSON(w, m.Snapshot())
}

// /watch SSE 订阅实时比分变化(把 versus 事件流桥接到 SSE)。
func (s *server) handleWatch(w http.ResponseWriter, r *http.Request) {
	m := s.currentMatch()
	if m == nil {
		http.Error(w, "没有进行中的 PK", http.StatusNotFound)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "不支持 SSE", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	ch, unsub := m.Subscribe(r.Context())
	defer unsub()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			b, _ := json.Marshal(ev)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, b)
			flusher.Flush()
			if ev.Type == versus.EventEnded {
				return
			}
		}
	}
}

func (s *server) currentMatch() *versus.Match {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.match
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func parseInt(s string, def int64) int64 {
	var n int64
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil || n == 0 {
		return def
	}
	return n
}
