// PWA 示例：Cache-Control + Idempotency-Key / 离线回放 + Web Push。
//
//	cd examples/pwa && go run .
//	打开 http://localhost:8088 （需 localhost 或 HTTPS 才能注册 SW / Push）
package main

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/contrib/webpush"
	"github.com/rushteam/beauty/pkg/api/offlinesync"
	"github.com/rushteam/beauty/pkg/middleware/httpcache"
	"github.com/rushteam/beauty/pkg/middleware/idempotencykey"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

//go:embed static/*
var staticFS embed.FS

func main() {
	keys, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		panic(err)
	}
	store := webpush.NewMemStore()
	pushClient := webpush.NewClient(keys, "mailto:pwa@beauty.local", webpush.WithStore(store))
	slog.Info("vapid public key", "key", keys.Public)

	var orders atomic.Int64
	// apiMux 仅挂业务 API，供 offlinesync 回放时二次分发。
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /api/public/feed", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ts":   time.Now().Format(time.RFC3339),
			"item": "hello-pwa",
		})
	})
	apiMux.Handle("POST /api/orders", idempotencykey.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := orders.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"order_id": n})
	})))

	mux := http.NewServeMux()
	mux.Handle("GET /api/public/feed", apiMux)
	mux.Handle("POST /api/orders", apiMux)
	mux.Handle("POST /sync", offlinesync.New(apiMux))
	mux.Handle("GET /push/vapid", webpush.VAPIDPublicHandler(pushClient))
	mux.Handle("/push/subscribe", webpush.SubscribeHandler(store, nil))
	mux.HandleFunc("POST /push/send", func(w http.ResponseWriter, r *http.Request) {
		user := r.URL.Query().Get("user")
		payload, _ := json.Marshal(map[string]string{"title": "Beauty", "body": "hello from server"})
		if err := pushClient.SendToUser(r.Context(), user, payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))
	mux.HandleFunc("GET /sw.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Service-Worker-Allowed", "/")
		w.Header().Set("Cache-Control", "no-cache")
		b, _ := staticFS.ReadFile("static/sw.js")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		b, _ := staticFS.ReadFile("static/index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", httpcache.AppShell.Header())
		_, _ = w.Write(b)
	})

	// fallback=NoStore：写 API / sync / push 默认不缓存；公开读与静态资源另配规则。
	handler := httpcache.Middleware(httpcache.NoStore,
		httpcache.Rule{Prefix: "/static/", Policy: httpcache.HashedAsset},
		httpcache.Rule{Prefix: "/api/public/", Policy: httpcache.PublicShort},
	)(mux)

	addr := envOr("ADDR", ":8088")
	app := beauty.New(beauty.WithWebServer(addr, handler, webserver.WithServiceName("pwa-demo")))
	slog.Info("pwa demo listening", "addr", addr)
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
