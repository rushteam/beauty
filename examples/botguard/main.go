// botguard 示例：浏览器风控签名（令牌下发 + 请求绑定签名 + 防重放 + 环境/行为评分 + 分级处置）。
//
// 启动后浏览器打开 http://localhost:8291 ，页面通过 BotGuard.fetch 调用受保护接口。
// 用 curl 直接请求 /api/ 会因缺少令牌返回 401；用 Headless Chrome 访问会被拦截或进入蜜罐。
//
// 降级开关演示：
//
//	curl -X POST 'http://localhost:8291/admin/mode?m=monitor'   # 只观测不拦截
//	curl -X POST 'http://localhost:8291/admin/mode?m=enforce'   # 恢复
package main

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/middleware/botguard"
	"github.com/rushteam/beauty/pkg/middleware/recovery"
	"github.com/rushteam/beauty/pkg/service/webserver"
	"github.com/rushteam/beauty/pkg/store/kvstore"
)

//go:embed index.html
var indexHTML []byte

func main() {
	store := kvstore.NewMemory() // 多实例部署请换成 Redis 实现的 kvstore.Store
	defer store.Stop()

	masterKey := make([]byte, 32) // 生产环境从 KMS / 配置中心读取，各实例一致
	_, _ = rand.Read(masterKey)

	g, err := botguard.New(store, masterKey,
		botguard.WithOnVerdict(func(r *http.Request, v *botguard.Verdict) {
			slog.Info("botguard", "path", r.URL.Path, "action", v.Action.String(),
				"failure", v.Failure, "score", v.Score, "reasons", v.Reasons, "cleared", v.Cleared)
		}),
		botguard.WithDeceive(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, map[string]any{"items": []string{"sample-1", "sample-2"}})
		})),
	)
	if err != nil {
		panic(err)
	}

	api := http.NewServeMux()
	api.HandleFunc("/api/items", func(w http.ResponseWriter, r *http.Request) {
		v, _ := botguard.FromContext(r.Context())
		writeJSON(w, map[string]any{"items": []string{"real-1", "real-2", "real-3"}, "score": v.Score, "reasons": v.Reasons})
	})

	root := http.NewServeMux()
	root.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexHTML)
	})
	root.Handle("GET /_botguard/sdk.js", botguard.SDKHandler())
	root.Handle("POST /_botguard/token", g.TokenHandler())
	root.Handle("/api/", g.Middleware()(api))
	root.HandleFunc("POST /admin/mode", func(w http.ResponseWriter, r *http.Request) {
		modes := map[string]botguard.Mode{"enforce": botguard.ModeEnforce, "monitor": botguard.ModeMonitor, "off": botguard.ModeOff}
		m, ok := modes[r.URL.Query().Get("m")]
		if !ok {
			http.Error(w, "m must be enforce|monitor|off", http.StatusBadRequest)
			return
		}
		g.SetMode(m)
		writeJSON(w, map[string]string{"mode": r.URL.Query().Get("m")})
	})

	app := beauty.New(beauty.WithWebServer(":8291", root,
		webserver.WithServiceName("botguard-demo"),
		webserver.WithMiddleware(recovery.HTTPMiddleware()),
	))
	fmt.Println("open http://localhost:8291")
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
