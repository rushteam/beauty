// matchmaker-room demo:matchmaker 匹配成功后分配 GameServer 地址。
//
// 默认 PoolAllocator(mock);设 BEAUTY_AGONES_ALLOCATOR=host:443 使用 gRPC Allocator。
//
// 运行:go run ./examples/matchmaker-room
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/contrib/agones"
	"github.com/rushteam/beauty/pkg/matchmaker"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

const listenAddr = "127.0.0.1:8288"

func main() {
	alloc, closeFn := openAllocator()
	if closeFn != nil {
		defer closeFn()
	}

	var mu sync.Mutex
	assignments := map[string]string{} // userID → host:port

	m := matchmaker.New(func(ctx context.Context, mm matchmaker.Match) error {
		result, err := alloc.Allocate(ctx, agones.AllocationRequest{
			Namespace: os.Getenv("BEAUTY_AGONES_NAMESPACE"),
			Metadata:  map[string]string{"players": fmt.Sprint(len(mm.Tickets))},
		})
		if err != nil {
			return err
		}
		mu.Lock()
		for _, t := range mm.Tickets {
			assignments[t.Presence.UserID] = result.Address
		}
		mu.Unlock()
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

	app := beauty.New(beauty.WithWebServer(listenAddr, mux, webserver.WithServiceName("matchmaker-room")))

	ctx, cancel := context.WithCancel(context.Background())
	appErr := make(chan error, 1)
	go func() { appErr <- app.Start(ctx) }()
	time.Sleep(80 * time.Millisecond)

	ok := runSelfTest()
	fmt.Println("──────── matchmaker-room 自测 ────────")
	if ok {
		fmt.Println("结论: ✅ 双人匹配 → 分配 game_addr + ws_url")
	} else {
		fmt.Println("结论: ❌ 自测失败")
	}

	cancel()
	<-appErr
	if !ok {
		os.Exit(1)
	}
}

func openAllocator() (agones.Allocator, func()) {
	if target := os.Getenv("BEAUTY_AGONES_ALLOCATOR"); target != "" {
		opts := []agones.GRPCOption{agones.WithAllocatorNamespace(envOr("BEAUTY_AGONES_NAMESPACE", "default"))}
		if os.Getenv("BEAUTY_AGONES_ALLOCATOR_INSECURE") == "1" {
			opts = append(opts, agones.WithAllocatorInsecure())
		} else if cert, key, ca := os.Getenv("BEAUTY_AGONES_ALLOCATOR_CERT"), os.Getenv("BEAUTY_AGONES_ALLOCATOR_KEY"), os.Getenv("BEAUTY_AGONES_ALLOCATOR_CA"); cert != "" {
			tlsCfg, err := agones.TLSConfigFromFiles(cert, key, ca)
			if err != nil {
				panic(err)
			}
			opts = append(opts, agones.WithAllocatorTLS(tlsCfg))
		}
		ga, err := agones.NewGRPCAllocator(target, opts...)
		if err != nil {
			panic(err)
		}
		return ga, func() { _ = ga.Close() }
	}
	addrs := []string{"127.0.0.1:8130"}
	if v := os.Getenv("BEAUTY_GAME_ADDRS"); v != "" {
		addrs = strings.Split(v, ",")
	}
	return agones.NewPoolAllocator(addrs), nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func runSelfTest() bool {
	client := &http.Client{Timeout: 2 * time.Second}
	base := "http://" + listenAddr

	queue := func(user string, skill float64) bool {
		u := fmt.Sprintf("%s/queue?user=%s&region=eu&skill=%g", base, user, skill)
		resp, err := client.Get(u)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}
	if !queue("alice", 1000) || !queue("bob", 1010) {
		return false
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(base + "/assign?user=alice")
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		var out map[string]string
		if json.Unmarshal(body, &out) != nil {
			return false
		}
		return out["game_addr"] != "" && strings.Contains(out["ws_url"], "alice")
	}
	return false
}
