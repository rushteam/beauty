// relationship 示例:社交图谱——好友 + 关注 + 拉黑。
//
// 演示 pkg/domain/relationship:AddFriend 双向好友、AddEdge 单向关注、
// Block 单向拉黑(好友请求被拒)、Outgoing 游标分页。
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/domain/relationship"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

func main() {
	g := relationship.New()
	var seq int64

	mux := http.NewServeMux()

	// /friend?a=alice&b=bob  双向好友。
	mux.HandleFunc("/friend", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		seq++
		if err := g.AddFriend(q.Get("a"), q.Get("b"), seq); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Write([]byte("friends"))
	})

	// /follow?from=alice&to=carol  单向关注。
	mux.HandleFunc("/follow", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		seq++
		if err := g.AddEdge(relationship.Edge{
			Source: q.Get("from"), Destination: q.Get("to"),
			State: relationship.StateActive, Position: seq,
		}); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Write([]byte("following"))
	})

	// /block?from=alice&to=dave  拉黑。
	mux.HandleFunc("/block", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		seq++
		g.Block(q.Get("from"), q.Get("to"), seq)
		w.Write([]byte("blocked"))
	})

	// /friends?user=alice  查双向好友。
	mux.HandleFunc("/friends", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(g.Friends(r.URL.Query().Get("user")))
	})

	// /following?user=alice&after=0&limit=10  查关注列表(游标分页)。
	mux.HandleFunc("/following", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
		limit, _ := strconv.Atoi(q.Get("limit"))
		list := g.Outgoing(q.Get("user"), after, limit, -1)
		names := make([]string, 0, len(list))
		for _, e := range list {
			names = append(names, e.Destination)
		}
		json.NewEncoder(w).Encode(names)
	})

	// /count?user=alice  查出边数。
	mux.HandleFunc("/count", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("edges=" + strconv.Itoa(g.Count(r.URL.Query().Get("user"), -1))))
	})

	app := beauty.New(beauty.WithWebServer(":8294", mux, webserver.WithServiceName("relationship-demo")))
	println("relationship demo on :8294")
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
