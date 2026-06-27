// presence 示例:在线状态追踪 + 频道成员列表。
//
// 演示 pkg/presence 的双索引:Track 登记在场、ListByStream 查成员、
// 事件总线回调 join/leave。HTTP 端点查询某频道的在线成员。
package main

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/presence"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

func main() {
	tr := presence.New(func(stream presence.Stream, joins, leaves []*presence.Presence) {
		for _, p := range joins {
			println("join:", stream.Subject, p.Meta.Username)
		}
		for _, p := range leaves {
			println("leave:", stream.Subject, p.Meta.Username)
		}
	}, 256)

	mux := http.NewServeMux()

	// /online?user=alice&session=s1&channel=room1  登记在场。
	mux.HandleFunc("/online", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		stream := presence.Stream{Mode: 1, Subject: q.Get("channel")}
		tr.Track(q.Get("session"), stream, presence.Meta{
			UserID:   q.Get("user"),
			Username: q.Get("user"),
		})
		w.Write([]byte("ok"))
	})

	// /offline?session=s1&channel=room1  移除在场。
	mux.HandleFunc("/offline", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		stream := presence.Stream{Mode: 1, Subject: q.Get("channel")}
		tr.Untrack(q.Get("session"), stream, q.Get("user"))
		w.Write([]byte("ok"))
	})

	// /members?channel=room1  查询频道在线成员。
	mux.HandleFunc("/members", func(w http.ResponseWriter, r *http.Request) {
		stream := presence.Stream{Mode: 1, Subject: r.URL.Query().Get("channel")}
		members := tr.ListByStream(stream, false)
		names := make([]string, 0, len(members))
		for _, m := range members {
			names = append(names, m.Meta.Username)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"channel":  stream.Subject,
			"count":    len(members),
			"members":  names,
		})
	})

	app := beauty.New(beauty.WithWebServer(":8283", mux, webserver.WithServiceName("presence-demo")))
	println("presence demo on :8283")
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
