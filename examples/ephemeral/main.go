// ephemeral 示例:短期 TTL KV(验证码 / 临时数据 / 缓存)。
//
// 演示 pkg/ephemeral:Set + TTL、Get、Delete、过期自动清理。
// 与 pkg/domain/storage 互补:storage 重(版本化+OCC),本包轻(纯内存+过期)。
package main

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/ephemeral"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

func main() {
	s := ephemeral.New()
	defer s.Stop()

	mux := http.NewServeMux()

	// /code?phone=138xxxx  生成验证码,5 分钟过期。
	mux.HandleFunc("/code", func(w http.ResponseWriter, r *http.Request) {
		phone := r.URL.Query().Get("phone")
		code := "123456" // demo 写死,实际用随机
		s.Set("code:"+phone, code, 5*time.Minute)
		w.Write([]byte("sent"))
	})

	// /verify?phone=138xxxx&code=123456  验证。
	mux.HandleFunc("/verify", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		v, ok := s.Get("code:" + q.Get("phone"))
		if !ok {
			http.Error(w, "expired or not found", http.StatusNotFound)
			return
		}
		if v != q.Get("code") {
			http.Error(w, "wrong code", http.StatusUnauthorized)
			return
		}
		w.Write([]byte("verified"))
	})

	// /count  当前条目数。
	mux.HandleFunc("/count", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("items=" + strconv.Itoa(s.Len())))
	})

	app := beauty.New(beauty.WithWebServer(":8302", mux, webserver.WithServiceName("ephemeral-demo")))
	println("ephemeral demo on :8302")
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
