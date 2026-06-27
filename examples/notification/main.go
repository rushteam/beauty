// notification 示例:离线通知队列,上线拉取。
//
// 漁示 pkg/notification:persistent 通知存库 + 在线即时投(通过 liveSink);
// 用户离线时通知留存,/list 拉取历史。与 pkg/router 互补:router 投在线,本包兜底离线。
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/domain/notification"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

func main() {
	// liveSink:模拟在线投递。生产可查 presence 判断在线后调 router.SendToPresenceIDs。
	var delivered atomic.Int32
	store := notification.New(func(uid string, n *notification.Notification) bool {
		// 这里恒返回 false(模拟离线),通知只存库。
		_ = uid
		_ = n
		return false
	}, notification.WithMaxPerUser(100))

	mux := http.NewServeMux()

	// /send?to=u1&subject=hello&persistent=1  发送持久通知。
	mux.HandleFunc("/send", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		persistent, _ := strconv.ParseBool(q.Get("persistent"))
		n := store.Send(r.Context(), &notification.Notification{
			UserID:     q.Get("to"),
			SenderID:   "system",
			Subject:    q.Get("subject"),
			Content:    q.Get("content"),
			Persistent: persistent || q.Get("persistent") == "", // 默认持久
		})
		json.NewEncoder(w).Encode(map[string]any{"id": nID(n), "delivered": delivered.Load()})
	})

	// /list?user=u1&after=0&limit=10  游标分页拉取。
	mux.HandleFunc("/list", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
		limit, _ := strconv.Atoi(q.Get("limit"))
		list := store.List(q.Get("user"), after, limit)
		json.NewEncoder(w).Encode(list)
	})

	// /count?user=u1  查未读数。
	mux.HandleFunc("/count", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]int{"count": store.Count(r.URL.Query().Get("user"))})
	})

	// 后台每 10 秒给 u1 发一条系统通知,演示"离线也留存"。
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		var seq int
		for range ticker.C {
			seq++
			store.Send(context.Background(), &notification.Notification{
				UserID:     "u1",
				Subject:    "system",
				Content:    `{"msg":"tick ` + strconv.Itoa(seq) + `"}`,
				Persistent: true,
			})
		}
	}()

	app := beauty.New(beauty.WithWebServer(":8290", mux, webserver.WithServiceName("notification-demo")))
	println("notification demo on :8290  (curl 'localhost:8290/send?to=u1&subject=hi')")
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}

func nID(n *notification.Notification) int64 {
	if n == nil {
		return 0
	}
	return n.ID
}
