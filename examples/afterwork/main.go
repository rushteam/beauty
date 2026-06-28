// afterwork + handler 示例:声明式 handler + 响应后副作用。
//
// 演示 supabase waitUntil 语义在 beauty 的落地:
//   - POST /order:用 pkg/handler 声明式包装(auth + inject + afterwork),
//     handler 返回响应后,afterwork.Defer 投递的"发邮件/写审计"在响应返回后跑完;
//   - GET /health:无 body、无 auth 的极简 handler。
//
// 跑:go run ./examples/afterwork
// 验证:curl -XPOST localhost:8303/order -d '{"sku":"A","qty":2}'
//       服务端日志会看到"response sent"先于"audit written"。
package main

import (
	"context"
	"log"
	"net/http"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/afterwork"
	perr "github.com/rushteam/beauty/pkg/errors"
	"github.com/rushteam/beauty/pkg/handler"
	"github.com/rushteam/beauty/pkg/middleware/auth"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

type orderReq struct {
	Sku string `json:"sku"`
	Qty int    `json:"qty"`
}
type orderResp struct {
	OrderID string `json:"order_id"`
}

type auditLog struct{}

func (auditLog) Write(ctx context.Context, event string) {
	log.Printf("[audit] %s", event)
}

func main() {
	audit := &auditLog{}

	createOrder := handler.New("POST",
		func(ctx context.Context, req *orderReq) (*orderResp, error) {
			user, _ := auth.GetUserFromContext(ctx)
			uid := "anonymous"
			if user != nil {
				uid = user.ID()
			}
			log.Printf("[handler] creating order user=%s sku=%s qty=%d", uid, req.Sku, req.Qty)
			// 响应后副作用:写审计、发通知。响应会立即返回,这些在返回后跑完。
			afterwork.Defer(ctx, func(c context.Context) {
				audit.Write(c, "order created user="+uid+" sku="+req.Sku)
				log.Printf("[afterwork] audit written (after response sent)")
			})
			log.Printf("[handler] response sent")
			return &orderResp{OrderID: "ord-1"}, nil
		},
		// 声明式:认证策略 + 依赖注入 + 响应后延寿。
		handler.WithAuth(func(ctx context.Context, r *http.Request) (auth.User, error) {
			tok := r.Header.Get("Authorization")
			if tok == "" {
				return nil, perr.New(perr.CodeUnauthenticated, "missing token")
			}
			// demo:直接把 token 当 user id。实际接 pkg/middleware/auth。
			return auth.NewUser(tok, tok, nil), nil
		}),
		handler.WithInject("audit", audit),
		handler.WithAfterwork(),
	)

	mux := http.NewServeMux()
	mux.Handle("/order", createOrder)
	mux.Handle("/health", handler.New("GET", func(ctx context.Context, req *struct{}) (*struct{}, error) {
		return nil, nil // 204
	}))

	app := beauty.New(beauty.WithWebServer(":8303", mux, webserver.WithServiceName("afterwork-demo")))
	log.Println("afterwork demo on :8303  (POST /order -H 'Authorization: u1' -d '{\"sku\":\"A\",\"qty\":2}')")
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
