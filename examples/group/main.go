// group + inbox + ratelimit 示例:群组离线私聊。
//
// 演示三个新包的组合:
//   - pkg/domain/group:群组创建/加入/角色(owner/admin/member);
//   - pkg/domain/inbox:成员间离线消息(已读/未读 + 游标分页);
//   - pkg/ratelimit:发消息限流(防刷屏,按 userID 令牌桶)。
//
// 路由:
//   POST /create?group=g1&owner=alice                  创建群组
//   POST /join?group=g1&user=bob                       加入群组
//   POST /msg?group=g1&from=alice&to=bob&text=hi       发私聊(限流 2/s)
//   GET  /inbox?user=bob&after=0&limit=10              拉收件箱(降序)
//   POST /read?user=bob&seq=3                          标记已读
//   GET  /unread?user=bob                              未读数
//   GET  /members?group=g1                             成员列表
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/domain/group"
	"github.com/rushteam/beauty/pkg/domain/inbox"
	"github.com/rushteam/beauty/pkg/ratelimit"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

var (
	groups  = group.New()
	inboxes = inbox.New(nil) // 无在线投递,纯离线留存
	tb      = ratelimit.NewTokenBucket(2, 2) // 2 令牌,2/s 补(每用户)
)

func main() {
	defer tb.Stop()
	mux := http.NewServeMux()

	mux.HandleFunc("/create", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		err := groups.Create(group.Group{
			ID: q.Get("group"), Name: q.Get("group"),
			OwnerID: q.Get("owner"), MaxMembers: 10,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Write([]byte("created"))
	})

	mux.HandleFunc("/join", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if err := groups.Join(q.Get("group"), q.Get("user")); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Write([]byte("joined"))
	})

	mux.HandleFunc("/members", func(w http.ResponseWriter, r *http.Request) {
		owners, admins, members, err := groups.Members(r.URL.Query().Get("group"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string][]string{
			"owner": owners, "admins": admins, "members": members,
		})
	})

	// 发消息:限流(每用户 2/s)+ 写收件箱。限流中间件直接挂在 mux。
	mux.Handle("/msg", ratelimit.Middleware(tb, func(r *http.Request) string {
		return r.URL.Query().Get("from") // 按发送者限流
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		// 校验 from 必须是群组成员。
		if _, _, err := groups.Role(q.Get("group"), q.Get("from")); err != nil {
			http.Error(w, "sender not a member", http.StatusForbidden)
			return
		}
		m := inboxes.Send(context.Background(),
			q.Get("to"), q.Get("from"), "chat", q.Get("text"))
		json.NewEncoder(w).Encode(m)
	})))

	mux.HandleFunc("/inbox", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
		limit, _ := strconv.Atoi(q.Get("limit"))
		list := inboxes.List(q.Get("user"), after, limit)
		if list == nil {
			list = []inbox.Message{}
		}
		json.NewEncoder(w).Encode(list)
	})

	mux.HandleFunc("/read", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		seq, _ := strconv.ParseInt(q.Get("seq"), 10, 64)
		inboxes.MarkOneRead(q.Get("user"), seq)
		w.Write([]byte("ok"))
	})

	mux.HandleFunc("/unread", func(w http.ResponseWriter, r *http.Request) {
		n := inboxes.UnreadCount(r.URL.Query().Get("user"))
		w.Write([]byte(strconv.Itoa(n)))
	})

	app := beauty.New(beauty.WithWebServer(":8304", mux, webserver.WithServiceName("group-demo")))
	println("group demo on :8304")
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
