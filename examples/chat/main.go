// chat 示例:频道消息持久化 + 游标分页历史拉取。
//
// 演示 pkg/domain/chat:Post 投递、Before 往前翻历史、After 增量拉新消息、
// Latest 最新条。与 pkg/domain/notification 互补(频道历史 vs 个人离线信)。
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/domain/chat"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

func main() {
	s := chat.New(chat.WithMaxPerChannel(500))

	mux := http.NewServeMux()

	// /post?channel=room1&user=alice&msg=hello  发消息。
	mux.HandleFunc("/post", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		m := s.Post(q.Get("channel"), q.Get("user"), q.Get("msg"), time.Now().UnixNano())
		if m == nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"id": m.ID, "msg_id": m.MsgID, "channel": m.ChannelID,
		})
	})

	// /history?channel=room1&before=0&limit=20  往前翻历史(before=0 取最新)。
	mux.HandleFunc("/history", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
		limit, _ := strconv.Atoi(q.Get("limit"))
		msgs := s.Before(q.Get("channel"), before, limit)
		json.NewEncoder(w).Encode(msgs)
	})

	// /new?channel=room1&after=5&limit=20  增量拉新消息(msgID > after)。
	mux.HandleFunc("/new", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
		limit, _ := strconv.Atoi(q.Get("limit"))
		msgs := s.After(q.Get("channel"), after, limit)
		json.NewEncoder(w).Encode(msgs)
	})

	// /count?channel=room1  当前消息数。
	mux.HandleFunc("/count", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("count=" + strconv.Itoa(s.Count(r.URL.Query().Get("channel")))))
	})

	app := beauty.New(beauty.WithWebServer(":8300", mux, webserver.WithServiceName("chat-demo")))
	println("chat demo on :8300")
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
