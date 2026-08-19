// 示例: HTTP 服务通过 Nacos 注册并被 Higress 自动发现。
//
// 服务注册后，Nacos metadata 中自动包含 protocol=HTTP，
// Higress 据此使用 HTTP 协议转发请求。
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/service/discover/nacos"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

func main() {
	nacosAddr := os.Getenv("NACOS_ADDR")
	if nacosAddr == "" {
		nacosAddr = "127.0.0.1:8848"
	}

	registry := nacos.NewRegistry(&nacos.Config{
		Addr:      []string{nacosAddr},
		Namespace: "public",
		Group:     "DEFAULT_GROUP",
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /web/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"service": "web-svc",
			"status":  "ok",
		})
	})
	mux.HandleFunc("GET /web/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("healthy"))
	})

	app := beauty.New(
		beauty.WithRegistry(registry),
		beauty.WithWebServer(":8080", mux,
			webserver.WithServiceName("web-svc"),
		),
	)

	slog.Info("web-svc starting", "nacos", nacosAddr, "listen", ":8080")
	if err := app.Start(context.Background()); err != nil {
		slog.Error("web-svc exited", "err", err)
		os.Exit(1)
	}
}
