// matchmaker-room demo:matchmaker 匹配成功后 mock 分配 GameServer 地址。
//
// 模拟 matchmaker → Allocator → 客户端连 agones-room 的链路;无需真实 K8s Agones Allocator。
//
// 运行:
//
//	# 终端 1: 游戏服(可开多个换端口)
//	go run ./examples/agones-room
//	# 终端 2: 匹配服
//	go run ./examples/matchmaker-room
//
//	curl "http://127.0.0.1:8288/queue?user=alice&region=eu&skill=1000"
//	curl "http://127.0.0.1:8288/queue?user=bob&region=eu&skill=1010"
//	curl http://127.0.0.1:8288/assign?user=alice
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/matchmaker"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

// Allocator 模拟 Agones GameServer 分配(轮询地址池)。
type Allocator struct {
	addrs []string
	idx   atomic.Uint64
}

func NewAllocator(addrs []string) *Allocator {
	if len(addrs) == 0 {
		addrs = []string{"127.0.0.1:8130"}
	}
	return &Allocator{addrs: addrs}
}

func (a *Allocator) Allocate() string {
	i := a.idx.Add(1)
	return a.addrs[int(i-1)%len(a.addrs)]
}

func main() {
	pool := NewAllocator(strings.Split(os.Getenv("BEAUTY_GAME_ADDRS"), ","))
	if os.Getenv("BEAUTY_GAME_ADDRS") == "" {
		pool = NewAllocator([]string{"127.0.0.1:8130"})
	}

	var mu sync.Mutex
	assignments := map[string]string{} // userID → host:port

	m := matchmaker.New(func(ctx context.Context, mm matchmaker.Match) error {
		addr := pool.Allocate()
		mu.Lock()
		for _, t := range mm.Tickets {
			assignments[t.Presence.UserID] = addr
		}
		mu.Unlock()
		println("allocated team to", addr, "players:", len(mm.Tickets))
		return nil
	}, matchmaker.WithTickInterval(300*time.Millisecond), matchmaker.WithMaxWaitSec(15))
	m.Start(context.Background())

	mux := http.NewServeMux()
	mux.HandleFunc("/queue", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		skill, _ := strconv.ParseFloat(q.Get("skill"), 64)
		_, err := m.Add(matchmaker.Ticket{
			Presence: matchmaker.Presence{UserID: q.Get("user"), SessionID: q.Get("user")},
			Properties: matchmaker.Properties{
				String:  map[string]string{"region": q.Get("region")},
				Numeric: map[string]float64{"skill": skill},
			},
			MinCount: 2, MaxCount: 2,
		}, "room", q.Get("region")+"|casual")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Write([]byte("queued"))
	})
	mux.HandleFunc("/assign", func(w http.ResponseWriter, r *http.Request) {
		user := r.URL.Query().Get("user")
		mu.Lock()
		addr, ok := assignments[user]
		mu.Unlock()
		if !ok {
			http.Error(w, "not assigned yet", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"game_addr": addr,
			"ws_url":    "ws://" + addr + "/ws?player=" + user,
		})
	})
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n := len(assignments)
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"waiting": m.Count(), "assigned": n})
	})

	app := beauty.New(beauty.WithWebServer(":8288", mux, webserver.WithServiceName("matchmaker-room")))
	println("matchmaker-room on :8288")
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
