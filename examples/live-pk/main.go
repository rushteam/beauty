// live-pk 组合 demo:多房间直播 PK 后端,展示多个原语如何协作。
//
// 组合的原语:
//   - pkg/versus       —— 每个 PK 房间:双方倒计时对抗计分、到点定胜负、事件流
//   - pkg/idempotency  —— 送礼幂等:同一 giftID 重复请求只计一次(防网络重试重复扣礼物)
//   - pkg/counter      —— 送礼配额:每用户每分钟送礼次数上限(防刷)
//   - pkg/tally        —— 高频人气聚合:点赞海量 +1 内存合并、批量落地
//   - pkg/keyedmutex   —— 按房间的细粒度锁:同房间 start/结算 串行,不同房间并行
//   - pkg/eventbus     —— 全局 PK 生命周期事件(开始/结束),供榜单/通知模块订阅
//   - pkg/idgen        —— 生成 PK 房间 ID
//
// 运行:go run ./examples/live-pk  然后:
//
//	curl localhost:8310/start                                  # 开一局 PK,返回 room
//	curl -N "localhost:8310/watch?room=<room>"                 # SSE 订阅该房间实时比分
//	curl "localhost:8310/gift?room=<room>&user=u1&side=A&val=100&gift=g1"
//	curl "localhost:8310/like?room=<room>"                     # 点赞(高频聚合)
//	curl "localhost:8310/snapshot?room=<room>"                 # 当前比分快照
//	curl localhost:8310/rooms                                  # 所有进行中的房间
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/rushteam/beauty/pkg/counter"
	"github.com/rushteam/beauty/pkg/eventbus"
	"github.com/rushteam/beauty/pkg/idempotency"
	"github.com/rushteam/beauty/pkg/idgen"
	"github.com/rushteam/beauty/pkg/keyedmutex"
	"github.com/rushteam/beauty/pkg/tally"
	"github.com/rushteam/beauty/pkg/versus"
)

// pkLifecycle 是 eventbus 上广播的全局 PK 生命周期事件负载。
type pkLifecycle struct {
	Room   string
	Winner string
	Scores map[string]int64
}

type server struct {
	ids       *idgen.Generator
	giftDedup *idempotency.Store[int64]
	quota     *counter.Counter
	likes     *tally.Tally[int64]
	roomLock  *keyedmutex.KeyedMutex     // 按房间串行 start/结算等结构性操作
	bus       *eventbus.Bus[pkLifecycle] // 全局 PK 生命周期事件

	mu    sync.RWMutex
	rooms map[string]*versus.Match // roomID → 房间(支持多局并行)
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
		roomLock:  keyedmutex.New(),
		bus:       eventbus.New[pkLifecycle](),
		rooms:     make(map[string]*versus.Match),
	}
	s.likes = tally.New(func(ctx context.Context, batch map[string]int64) {
		for room, n := range batch {
			fmt.Printf("[tally] 房间 %s 点赞 +%d 批量落地\n", room, n)
		}
	}, tally.WithFlushInterval(2*time.Second))
	defer s.giftDedup.Stop()
	defer s.quota.Stop()
	defer s.likes.Stop()

	// 订阅全局 PK 事件:演示榜单/通知等下游模块如何解耦接入。
	s.bus.Subscribe("pk.ended", func(topic string, e pkLifecycle) {
		fmt.Printf("[eventbus] 房间 %s 结束,胜者=%q 比分=%v(→ 可推送通知/更新榜单)\n", e.Room, e.Winner, e.Scores)
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/start", s.handleStart)
	mux.HandleFunc("/gift", s.handleGift)
	mux.HandleFunc("/like", s.handleLike)
	mux.HandleFunc("/snapshot", s.handleSnapshot)
	mux.HandleFunc("/watch", s.handleWatch)
	mux.HandleFunc("/rooms", s.handleRooms)

	fmt.Println("live-pk demo 监听 :8310")
	fmt.Println("  /start  /gift?room=&user=&side=&val=&gift=  /like?room=  /snapshot?room=  /watch?room=  /rooms")
	http.ListenAndServe(":8310", mux)
}

// /start 开一局新 PK,返回房间 ID(支持多局并行)。
func (s *server) handleStart(w http.ResponseWriter, r *http.Request) {
	id := fmt.Sprintf("pk-%d", s.ids.MustNext())

	// 按房间加锁:同房间的结构性操作串行(此处 start 是新房间,主要演示 keyedmutex 用法)。
	unlock := s.roomLock.Lock(id)
	defer unlock()

	m := versus.New(id, []string{"A", "B"},
		versus.WithDuration(pkDuration),
		versus.WithOnEnd(func(res versus.Result) {
			// 到点结算:广播全局事件 + 清理房间(延迟一会儿给 SSE 收尾)。
			s.bus.Publish("pk.ended", pkLifecycle{Room: res.ID, Winner: res.Winner, Scores: res.Scores})
			s.removeRoomLater(res.ID)
		}))
	s.mu.Lock()
	s.rooms[id] = m
	s.mu.Unlock()

	_ = m.Start()
	s.bus.Publish("pk.started", pkLifecycle{Room: id})
	writeJSON(w, map[string]any{"room": id, "duration": pkDuration.String()})
}

// /gift 送礼加分:配额防刷 + 幂等去重 + 计分。
func (s *server) handleGift(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	room, user, side, giftID := q.Get("room"), q.Get("user"), q.Get("side"), q.Get("gift")
	val := parseInt(q.Get("val"), 1)
	if user == "" || (side != "A" && side != "B") || giftID == "" {
		http.Error(w, "需要 room, user, side(A|B), gift, val", http.StatusBadRequest)
		return
	}
	m := s.room(room)
	if m == nil {
		http.Error(w, "房间不存在或已结束", http.StatusNotFound)
		return
	}

	// 1) 配额:每用户每分钟送礼次数上限(防刷)。
	if !s.quota.Allow("gift:"+user, 1, giftQuotaPerMin) {
		http.Error(w, "送礼太频繁,请稍后", http.StatusTooManyRequests)
		return
	}

	// 2) 幂等:同一 giftID 重复请求只计一次(网络重试安全)。giftID 全局唯一,含房间维度。
	score, err, shared := s.giftDedup.Do("gift:"+room+":"+giftID, func() (int64, error) {
		return m.Add(side, val)
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, map[string]any{
		"room": room, "side": side, "side_score": score,
		"replayed": shared, // true=重复请求,未重复加分
	})
}

// /like 点赞:高频,只做聚合计数(不逐笔落地)。
func (s *server) handleLike(w http.ResponseWriter, r *http.Request) {
	room := r.URL.Query().Get("room")
	if s.room(room) == nil {
		http.Error(w, "房间不存在或已结束", http.StatusNotFound)
		return
	}
	s.likes.Add(room, 1)
	w.WriteHeader(http.StatusNoContent)
}

// /snapshot 某房间当前比分快照。
func (s *server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	m := s.room(r.URL.Query().Get("room"))
	if m == nil {
		http.Error(w, "房间不存在或已结束", http.StatusNotFound)
		return
	}
	writeJSON(w, m.Snapshot())
}

// /rooms 列出所有进行中的房间及其比分。
func (s *server) handleRooms(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	out := make([]versus.Snapshot, 0, len(s.rooms))
	for _, m := range s.rooms {
		out = append(out, m.Snapshot())
	}
	s.mu.RUnlock()
	writeJSON(w, out)
}

// /watch SSE 订阅某房间实时比分变化(把 versus 事件流桥接到 SSE)。
func (s *server) handleWatch(w http.ResponseWriter, r *http.Request) {
	m := s.room(r.URL.Query().Get("room"))
	if m == nil {
		http.Error(w, "房间不存在或已结束", http.StatusNotFound)
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

// room 按 ID 取房间(读锁)。
func (s *server) room(id string) *versus.Match {
	if id == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rooms[id]
}

// removeRoomLater 结算后延迟清理房间(给 /watch 收尾时间),用 keyedmutex 串行化清理。
func (s *server) removeRoomLater(id string) {
	go func() {
		time.Sleep(2 * time.Second)
		unlock := s.roomLock.Lock(id)
		defer unlock()
		s.mu.Lock()
		if m, ok := s.rooms[id]; ok {
			m.Close()
			delete(s.rooms, id)
		}
		s.mu.Unlock()
	}()
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
