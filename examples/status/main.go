// status 示例:用户上下线时给关注者推送状态通知。
//
// 演示 pkg/presence/status:订阅 presence 事件,查 relationship.Watchers 找关注者,
// 走 router 投递 status notification。串起 presence + relationship + router。
//
// 流程:
//
//	/friend?a=alice&b=bob     建立双向好友(互为关注者)
//	/join?session=s1&user=alice&channel=room1   alice 加入流 → bob 收到 online 通知
//	/leave?session=s1&user=alice&channel=room1  alice 离开 → bob 收到 offline 通知
package main

import (
	"context"
	"net/http"
	"strconv"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/domain/relationship"
	"github.com/rushteam/beauty/pkg/presence"
	"github.com/rushteam/beauty/pkg/presence/status"
	"github.com/rushteam/beauty/pkg/router"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

type sinkRegistry struct {
	sinks map[string]router.Sink
}

func (r *sinkRegistry) Lookup(sid string) router.Sink { return r.sinks[sid] }

func main() {
	g := relationship.New()
	regs := &sinkRegistry{sinks: make(map[string]router.Sink)}
	rtr := router.New(regs, nil)

	// status dispatcher:presence 事件 → graph.Watchers → router 投递。
	// presenceStore 传 nil:userStreams 由 dispatcher 内部 online map 维护,
	// 不需要额外查 tracker。
	disp := status.New(
		status.WithWatcherFinder(g),
		status.WithNotifier(func(sids []string, p []byte) int {
			return rtr.SendToSessionIDs(sids, router.Message{Data: p, Reliable: true})
		}),
	)
	// 用 disp.OnPresence 作 listener 启动 presence tracker。
	tr := presence.New(disp.OnPresence, 256)

	mux := http.NewServeMux()

	// /friend?a=alice&b=bob  建立双向好友。
	var seq int64
	mux.HandleFunc("/friend", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		seq++
		if err := g.AddFriend(q.Get("a"), q.Get("b"), seq); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Write([]byte("friends"))
	})

	// /join?session=s1&user=alice&channel=room1  加入流(触发 online 通知)。
	mux.HandleFunc("/join", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		sid := q.Get("session")
		stream := presence.Stream{Mode: 1, Subject: q.Get("channel")}
		tr.Track(sid, stream, presence.Meta{UserID: q.Get("user"), Username: q.Get("user")})
		// 注册 sink:把投递打印出来(模拟 ws.Send)。
		regs.sinks[sid] = func(m router.Message) bool {
			println("deliver to", sid, ":", string(m.Data))
			return true
		}
		w.Write([]byte("joined"))
	})

	// /leave?session=s1&user=alice&channel=room1  离开流(可能触发 offline)。
	mux.HandleFunc("/leave", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		stream := presence.Stream{Mode: 1, Subject: q.Get("channel")}
		tr.Untrack(q.Get("session"), stream, q.Get("user"))
		w.Write([]byte("left"))
	})

	// /watchers?user=alice  查谁关注了 alice。
	mux.HandleFunc("/watchers", func(w http.ResponseWriter, r *http.Request) {
		ws := g.Watchers(r.URL.Query().Get("user"), -1)
		w.Write([]byte(strconv.Itoa(len(ws)) + " watchers"))
	})

	app := beauty.New(beauty.WithWebServer(":8299", mux, webserver.WithServiceName("status-demo")))
	println("status demo on :8299")
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
