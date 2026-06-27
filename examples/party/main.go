// party 示例:无权威小队,Leader 审核 + 状态广播。
//
// 演示 pkg/party:创建派对、请求加入、Leader Accept/Remove/Promote,
// 每次变更广播快照(此处打印,生产可接 router.SendToStream)。
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/domain/party"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

var (
	mu      sync.Mutex
	parties = map[string]*party.Party{}
)

func getParty(id string) *party.Party {
	mu.Lock()
	defer mu.Unlock()
	return parties[id]
}

func main() {
	// 预置一个派对,leader=alice。
	onChange := func(s party.Snapshot) {
		println("[party", s.ID, "] leader=", s.LeaderID, "members=", len(s.Members), "requests=", len(s.JoinRequests))
	}
	p := party.New("room1", party.Member{UserID: "alice", Username: "Alice"}, onChange,
		party.WithOpen(false), party.WithMaxSize(4),
	)
	mu.Lock()
	parties["room1"] = p
	mu.Unlock()

	mux := http.NewServeMux()

	// /join?party=room1&user=bob  请求加入(private 需 leader Accept)。
	mux.HandleFunc("/join", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		pp := getParty(q.Get("party"))
		if pp == nil {
			http.Error(w, "no party", http.StatusNotFound)
			return
		}
		if err := pp.RequestJoin(party.Member{UserID: q.Get("user"), Username: q.Get("user")}); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Write([]byte("requested"))
	})

	// /accept?party=room1&leader=alice&user=bob  Leader 接受。
	mux.HandleFunc("/accept", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		pp := getParty(q.Get("party"))
		if err := pp.Accept(q.Get("leader"), q.Get("user")); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Write([]byte("accepted"))
	})

	// /kick?party=room1&leader=alice&user=bob  Leader 踢人。
	mux.HandleFunc("/kick", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		pp := getParty(q.Get("party"))
		if err := pp.Remove(q.Get("leader"), q.Get("user")); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Write([]byte("removed"))
	})

	// /promote?party=room1&leader=alice&to=bob  转让队长。
	mux.HandleFunc("/promote", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		pp := getParty(q.Get("party"))
		if err := pp.Promote(q.Get("leader"), q.Get("to")); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Write([]byte("promoted"))
	})

	// /state?party=room1  查当前快照。
	mux.HandleFunc("/state", func(w http.ResponseWriter, r *http.Request) {
		pp := getParty(r.URL.Query().Get("party"))
		if pp == nil {
			http.Error(w, "no party", http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(pp.Snapshot())
	})

	app := beauty.New(beauty.WithWebServer(":8292", mux, webserver.WithServiceName("party-demo")))
	println("party demo on :8292")
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
