// Package bulkhead 是 pkg/resilience/bulkhead 的 HTTP 中间件封装:给一组路由设并发配额,
// 进来的请求先抢并发槽位,满了(且队列也满/超时)就快速返回 503,保护后端资源不被慢请求
// 堆积拖垮。与 middleware/circuitbreaker(按错误率熔断)、middleware/ratelimit(按速率限流)
// 并列,三者可叠加在同一条链上。
package bulkhead

import (
	"net/http"

	"github.com/rushteam/beauty/pkg/resilience/bulkhead"
)

// Config 配置 HTTP 隔离舱中间件。
type Config struct {
	// OnRejected 自定义被拒绝(并发满/超时/取消)时的响应;为 nil 时默认返回 503。
	OnRejected func(w http.ResponseWriter, r *http.Request, err error)
	// RetryAfterSeconds 若 >0,拒绝时写入 Retry-After 响应头(秒)。
	RetryAfterSeconds int
}

// HTTPMiddleware 用给定隔离舱返回一个 HTTP 中间件。bh 通常由 bulkhead.New(maxConcurrent, ...)
// 构造(可带排队与获取超时)。多个路由共享同一个 bh 即共享同一份并发配额。
func HTTPMiddleware(bh *bulkhead.Bulkhead, cfg ...Config) func(next http.Handler) http.Handler {
	var c Config
	if len(cfg) > 0 {
		c = cfg[0]
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			release, err := bh.Acquire(r.Context())
			if err != nil {
				if c.RetryAfterSeconds > 0 {
					w.Header().Set("Retry-After", itoa(c.RetryAfterSeconds))
				}
				if c.OnRejected != nil {
					c.OnRejected(w, r, err)
					return
				}
				http.Error(w, "Service Unavailable: bulkhead is full", http.StatusServiceUnavailable)
				return
			}
			defer release()
			next.ServeHTTP(w, r)
		})
	}
}

// itoa 把非负整数转字符串(避免引入 strconv 仅为一处用途)。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
