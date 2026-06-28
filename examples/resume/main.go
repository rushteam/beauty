// resume 示例:断线重连的在场状态还原。
//
// 演示 pkg/resume:客户端用 refresh token 重连 → 服务端换出 userID + 还在的流列表
// → 客户端按列表自动重新 join。串起 pkg/token + pkg/presence。
//
// 流程:
//   /login       签发 dual token,模拟已加入 room1/party-a
//   /reconnect   用 refresh token 还原在场流列表
//   /remark      用新 sessionID 把流重新登记(MarkOnline)
package main

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/presence"
	"github.com/rushteam/beauty/pkg/resume"
	"github.com/rushteam/beauty/pkg/service/webserver"
	"github.com/rushteam/beauty/pkg/token"
)

var (
	tm   *token.Manager
	tr   *presence.Tracker
	rsv  *resume.Resolver
)

func main() {
	tm = token.New(
		token.WithSessionKey([]byte("demo-session-key-32-bytes-aaaa")),
		token.WithRefreshKey([]byte("demo-refresh-key-32-bytes-bbb")),
	)
	defer tm.Stop()
	tr = presence.New(nil, 256)
	rsv = resume.New(resume.WithTokenManager(tm), resume.WithTracker(tr))

	mux := http.NewServeMux()

	// /login?user=alice  签发 dual token,模拟断线前已加入 room1 + party-a。
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		user := r.URL.Query().Get("user")
		sess, refresh, err := tm.Issue(user, user, nil, "")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		// 用 tokenID 作为 sessionID 登记到两个流(模拟断线前的在场状态)。
		c, _ := tm.Verify(sess)
		tr.Track(c.TokenID, presence.Stream{Mode: 1, Subject: "room1"}, presence.Meta{UserID: user})
		tr.Track(c.TokenID, presence.Stream{Mode: 2, Subject: "party-a"}, presence.Meta{UserID: user})
		json.NewEncoder(w).Encode(map[string]string{"session": sess, "refresh": refresh})
	})

	// /reconnect?refresh=...  用 refresh token 还原在场流列表。
	mux.HandleFunc("/reconnect", func(w http.ResponseWriter, r *http.Request) {
		info, err := rsv.Resolve(r.URL.Query().Get("refresh"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		// 用新 sessionID 重新登记(MarkOnline),模拟客户端用新连接重连。
		newSID := "new-" + info.TokenID
		rsv.MarkOnline(newSID, info.UserID, info.UserID, info.Streams, false)
		json.NewEncoder(w).Encode(map[string]any{
			"user":         info.UserID,
			"new_session":  newSID,
			"streams":      info.Streams,
		})
	})

	app := beauty.New(beauty.WithWebServer(":8298", mux, webserver.WithServiceName("resume-demo")))
	println("resume demo on :8298")
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
