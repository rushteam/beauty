// Package bodylimit 提供 HTTP 请求体大小限制中间件，防止客户端发送过大 body
// 耗尽服务端内存。超限时返回 413 Request Entity Too Large。
package bodylimit

import (
	"net/http"
)

// Middleware 返回限制请求体大小的 HTTP 中间件。
// maxBytes <= 0 时不限制（透传）。
//
//	mux.Use(bodylimit.Middleware(10 << 20)) // 10 MB
func Middleware(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if maxBytes <= 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}
