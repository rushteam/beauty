// api-security 示例：AntiReplay + SignVerify + TrustedHeader 三种安全中间件的组合用法。
//
// 启动后可用 curl 测试（见下方打印的 curl 命令），演示三种典型场景：
//
//   - /payment/*  完整安全链：防重放 + 签名校验 + 用户身份提取
//   - /wallet/*   签名校验 + 用户身份提取（无防重放）
//   - /order/*    信任网关 header（仅提取用户身份）
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/kvstore"
	"github.com/rushteam/beauty/pkg/middleware/antireplay"
	"github.com/rushteam/beauty/pkg/middleware/auth"
	"github.com/rushteam/beauty/pkg/middleware/recovery"
	"github.com/rushteam/beauty/pkg/middleware/signverify"
	"github.com/rushteam/beauty/pkg/service/webserver"
)

// ---- 业务 handler ----

func payHandler(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.GetUserFromContext(r.Context())
	writeJSON(w, map[string]string{
		"action":  "payment created",
		"user_id": user.ID(),
	})
}

func balanceHandler(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.GetUserFromContext(r.Context())
	writeJSON(w, map[string]string{
		"balance": "99.50",
		"user_id": user.ID(),
	})
}

func orderHandler(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.GetUserFromContext(r.Context())
	writeJSON(w, map[string]string{
		"order_id": "ORD-2024001",
		"user_id":  user.ID(),
	})
}

// ---- 配置 ----

var appSecrets = map[string][]byte{
	"pay-app":  []byte("secret-for-pay"),
	"mall-bff": []byte("secret-for-mall"),
}

func getSecret(appID string) ([]byte, bool) {
	s, ok := appSecrets[appID]
	return s, ok
}

func main() {
	nonceStore := kvstore.NewMemory()
	defer nonceStore.Stop()

	// ---- 场景 1：Payment（完整安全链）----
	paymentMux := http.NewServeMux()
	paymentMux.HandleFunc("/payment/create", payHandler)

	paymentHandler := chain(paymentMux,
		antireplay.HTTPMiddleware(nonceStore,
			antireplay.WithSkipPrefixes("/healthz"),
		),
		signverify.HTTPMiddleware(getSecret,
			signverify.WithExtractUser(),
			signverify.WithSkipPrefixes("/healthz"),
		),
	)

	// ---- 场景 2：Wallet（签名校验，无防重放）----
	walletMux := http.NewServeMux()
	walletMux.HandleFunc("/wallet/balance", balanceHandler)

	walletHandler := chain(walletMux,
		signverify.HTTPMiddleware(getSecret,
			signverify.WithExtractUser(),
		),
	)

	// ---- 场景 3：Order（信任网关 header）----
	orderMux := http.NewServeMux()
	orderMux.HandleFunc("/order/detail", orderHandler)

	orderAuthMW := auth.NewAuthMiddleware(auth.Config{
		TokenExtractor: auth.NewHeaderTokenExtractor("X-User-Id", ""),
		Authenticator:  auth.NewTrustedHeaderAuthenticator(),
	})
	orderHandler := chain(orderMux,
		auth.HTTPMiddleware(orderAuthMW),
	)

	// ---- 组合路由 ----
	root := http.NewServeMux()
	root.Handle("/payment/", paymentHandler)
	root.Handle("/wallet/", walletHandler)
	root.Handle("/order/", orderHandler)
	root.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})

	printUsage()

	app := beauty.New(beauty.WithWebServer(":8290", root,
		webserver.WithServiceName("api-security-demo"),
		webserver.WithMiddleware(recovery.HTTPMiddleware()),
	))
	fmt.Println("server listening on :8290")
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}

// chain 按顺序包装中间件（第一个参数最外层）。
func chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func printUsage() {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	body := `{"amount":100}`

	paySig := signverify.Sign(appSecrets["pay-app"], ts, "user-42", []byte(body))
	walletSig := signverify.Sign(appSecrets["mall-bff"], ts, "user-42", nil)

	fmt.Println(`
=== API Security Demo ===

--- 场景 1：Payment（防重放 + 签名 + 身份）---
curl -X POST http://localhost:8290/payment/create \
  -H "X-App-Id: pay-app" \
  -H "X-Timestamp: ` + ts + `" \
  -H "X-User-Id: user-42" \
  -H "X-Nonce: nonce-001" \
  -H "X-Sign: ` + paySig + `" \
  -d '` + body + `'

(再发一次相同 nonce 会返回 403 replay detected)

--- 场景 2：Wallet（签名 + 身份，无防重放）---
curl http://localhost:8290/wallet/balance \
  -H "X-App-Id: mall-bff" \
  -H "X-Timestamp: ` + ts + `" \
  -H "X-User-Id: user-42" \
  -H "X-Sign: ` + walletSig + `"

--- 场景 3：Order（信任网关 header）---
curl http://localhost:8290/order/detail \
  -H "X-User-Id: user-42"

--- 健康检查（跳过所有安全中间件）---
curl http://localhost:8290/healthz
`)
}
