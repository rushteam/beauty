// token 示例:会话令牌签发、续签、注销全流程。
//
// 演示 pkg/token:dual token(Issue session+refresh)、Verify、Refresh 续签、
// Revoke 单 token 注销、RevokeAll 全局踢出。配合 /login、/refresh、/logout、/kick 路由。
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/service/webserver"
	"github.com/rushteam/beauty/pkg/token"
)

var mgr *token.Manager

type tokenResp struct {
	Session string `json:"session"`
	Refresh string `json:"refresh"`
}

func main() {
	mgr = token.New(
		token.WithSessionKey([]byte("demo-session-key-32-bytes-aaaa")),
		token.WithRefreshKey([]byte("demo-refresh-key-32-bytes-bbb")),
		token.WithSessionTTL(time.Minute),
		token.WithRefreshTTL(10*time.Minute),
	)
	defer mgr.Stop()

	mux := http.NewServeMux()

	// /login?user=alice  签发 dual token。
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		user := r.URL.Query().Get("user")
		sess, refresh, err := mgr.Issue(user, user, map[string]string{"role": "player"}, "")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(tokenResp{Session: sess, Refresh: refresh})
	})

	// /verify?token=...  验证 session token。
	mux.HandleFunc("/verify", func(w http.ResponseWriter, r *http.Request) {
		c, err := mgr.Verify(r.URL.Query().Get("token"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"user": c.UserID, "vars": c.Vars, "token_id": c.TokenID,
		})
	})

	// /refresh?token=...  用 refresh token 换新 session。
	mux.HandleFunc("/refresh", func(w http.ResponseWriter, r *http.Request) {
		sess, err := mgr.Refresh(r.URL.Query().Get("token"), nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"session": sess})
	})

	// /logout?token=...  按 tokenID 注销单会话。
	mux.HandleFunc("/logout", func(w http.ResponseWriter, r *http.Request) {
		c, err := mgr.Verify(r.URL.Query().Get("token"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		mgr.Revoke(c.TokenID)
		w.Write([]byte("logged out"))
	})

	// /kick?user=alice  踢出该用户所有会话。
	mux.HandleFunc("/kick", func(w http.ResponseWriter, r *http.Request) {
		mgr.RevokeAll(r.URL.Query().Get("user"))
		w.Write([]byte("kicked"))
	})

	app := beauty.New(beauty.WithWebServer(":8295", mux, webserver.WithServiceName("token-demo")))
	println("token demo on :8295")
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
