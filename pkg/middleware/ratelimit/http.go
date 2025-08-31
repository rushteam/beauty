package ratelimit

import (
	"context"
	"fmt"
	"net/http"
)

// HTTPMiddleware 返回 HTTP 限流中间件
func HTTPMiddleware(rl *RateLimitMiddleware) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 构建元数据
			metadata := buildHTTPMetadata(r)

			// 执行限流检查
			err := rl.Allow(r.Context(), metadata)
			if err != nil {
				handleRateLimitError(w, err)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// HTTPWaitMiddleware 返回等待型 HTTP 限流中间件（会等待而不是直接拒绝）
func HTTPWaitMiddleware(rl *RateLimitMiddleware) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 构建元数据
			metadata := buildHTTPMetadata(r)

			// 等待限流通过
			err := rl.Wait(r.Context(), metadata)
			if err != nil {
				handleRateLimitError(w, err)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// buildHTTPMetadata 构建 HTTP 请求元数据
func buildHTTPMetadata(r *http.Request) map[string]interface{} {
	metadata := make(map[string]interface{})

	// 添加 headers
	headers := make(map[string][]string)
	for name, values := range r.Header {
		headers[name] = values
	}
	metadata["headers"] = headers

	// 添加查询参数
	metadata["query"] = r.URL.Query()

	// 添加其他信息
	metadata["method"] = r.Method
	metadata["path"] = r.URL.Path
	metadata["remote_addr"] = r.RemoteAddr
	metadata["user_agent"] = r.UserAgent()
	metadata["host"] = r.Host

	// 添加用户信息（如果存在）
	if user := r.Context().Value("user"); user != nil {
		metadata["user"] = user
	}

	return metadata
}

// handleRateLimitError 处理限流错误
func handleRateLimitError(w http.ResponseWriter, err error) {
	if err == ErrRateLimitExceeded {
		w.Header().Set("X-RateLimit-Limit", "exceeded")
		http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
	} else if err == context.DeadlineExceeded {
		http.Error(w, "Request timeout while waiting for rate limit", http.StatusRequestTimeout)
	} else {
		http.Error(w, "Rate limit error", http.StatusInternalServerError)
	}
}

// HTTPClientMiddleware 返回 HTTP 客户端限流中间件
func HTTPClientMiddleware(rl *RateLimitMiddleware) func(http.RoundTripper) http.RoundTripper {
	return func(next http.RoundTripper) http.RoundTripper {
		if next == nil {
			next = http.DefaultTransport
		}

		return &rateLimitTransport{
			rl:   rl,
			next: next,
		}
	}
}

// rateLimitTransport 实现 http.RoundTripper 接口
type rateLimitTransport struct {
	rl   *RateLimitMiddleware
	next http.RoundTripper
}

func (t *rateLimitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// 构建元数据
	metadata := buildHTTPMetadata(req)

	// 执行限流检查
	err := t.rl.Allow(req.Context(), metadata)
	if err != nil {
		return nil, err
	}

	return t.next.RoundTrip(req)
}

// RequireRateLimit 创建需要限流检查的中间件（用于特定路由）
func RequireRateLimit(rl *RateLimitMiddleware) func(http.Handler) http.Handler {
	return HTTPMiddleware(rl)
}

// OptionalRateLimit 创建可选限流中间件（限流失败不会阻止请求，但会记录）
func OptionalRateLimit(rl *RateLimitMiddleware) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			metadata := buildHTTPMetadata(r)

			// 尝试限流检查，但不阻止请求
			err := rl.Allow(r.Context(), metadata)
			if err != nil {
				// 添加限流状态头部
				w.Header().Set("X-RateLimit-Status", "exceeded")
			} else {
				w.Header().Set("X-RateLimit-Status", "ok")
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RateLimitInfo 限流信息中间件（添加限流状态头部）
func RateLimitInfo(rl *RateLimitMiddleware) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			metadata := buildHTTPMetadata(r)
			key := rl.extractKey(r.Context(), metadata)
			limiter := rl.getLimiter(key)

			// 添加限流信息头部
			w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%.0f", rl.LimitRate()))
			w.Header().Set("X-RateLimit-Burst", fmt.Sprintf("%d", rl.Burst()))
			w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", limiter.Tokens()))

			next.ServeHTTP(w, r)
		})
	}
}

// IsHTTPRateLimitError 检查错误是否为 HTTP 限流错误
func IsHTTPRateLimitError(err error) bool {
	return IsRateLimitError(err)
}
