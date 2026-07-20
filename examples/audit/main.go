// audit 示例:操作审计,仅记成功请求。
//
// 演示 pkg/audit:HTTPMiddleware 包裹业务 handler,2xx/4xx 记一条审计到 Sink,
// 5xx 不记(走 logger)。Sink 这里用内存打印,生产可换 DB。
package main

import (
	"context"
	"net/http"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/audit"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

const (
	resUser   audit.Resource = iota + 1 // 用户资源
	resConfig                           // 配置资源
)

func main() {
	// 内存 sink:打印每条审计。
	sink := audit.SinkFunc(func(ctx context.Context, e audit.Entry) error {
		println("AUDIT id=", e.ID, "user=", e.UserID, "res=", e.ResourceID,
			"action=", e.Action, "status=", e.Status, "meta=", e.Metadata)
		return nil
	})
	a := audit.New(sink)
	defer a.Stop()

	resolver := func(r *http.Request) (audit.Resource, string, string) {
		if r.URL.Path == "/users" {
			return resUser, r.URL.Query().Get("id"), `{"src":"demo"}`
		}
		return resConfig, r.URL.Path, ""
	}

	mux := http.NewServeMux()
	mux.Handle("/users", a.HTTPMiddleware(resolver)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 从 ctx 取 userID(生产由 auth 中间件注入)。
		w.Write([]byte("ok"))
	})))
	mux.Handle("/config", a.HTTPMiddleware(resolver)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})))

	// 把 userID 注入 ctx 的示例中间件。
	wrap := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h.ServeHTTP(w, r.WithContext(audit.WithUserID(r.Context(), "admin")))
		})
	}

	app := beauty.New(beauty.WithWebServer(":8289", wrap(mux), webserver.WithServiceName("audit-demo")))
	println("audit demo on :8289  (curl localhost:8289/users?id=u1)")
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
