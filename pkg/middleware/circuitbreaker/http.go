package circuitbreaker

import (
	"net/http"
)

// HTTPMiddleware 返回一个 HTTP 中间件，用于熔断
func HTTPMiddleware(cb *CircuitBreaker) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			err := cb.Call(func() error {
				// 创建一个响应写入器来捕获状态码
				rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
				next.ServeHTTP(rw, r)

				// 根据状态码判断是否为错误
				if rw.statusCode >= 500 {
					return &HTTPError{StatusCode: rw.statusCode}
				}
				return nil
			})

			if err != nil {
				if err == ErrCircuitBreakerOpen {
					http.Error(w, "Service Unavailable: Circuit breaker is open", http.StatusServiceUnavailable)
					return
				}
				if err == ErrTooManyRequests {
					http.Error(w, "Too Many Requests: Circuit breaker in half-open state", http.StatusTooManyRequests)
					return
				}
				// 如果是 HTTPError，说明已经处理过了
				if _, ok := err.(*HTTPError); ok {
					return
				}
				// 其他错误
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		})
	}
}

// HTTPError HTTP 错误类型
type HTTPError struct {
	StatusCode int
}

func (e *HTTPError) Error() string {
	return http.StatusText(e.StatusCode)
}

// responseWriter 包装 http.ResponseWriter 以捕获状态码
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.written {
		rw.statusCode = code
		rw.written = true
		rw.ResponseWriter.WriteHeader(code)
	}
}

func (rw *responseWriter) Write(data []byte) (int, error) {
	if !rw.written {
		rw.WriteHeader(http.StatusOK)
	}
	return rw.ResponseWriter.Write(data)
}

// HTTPClientMiddleware 返回一个 HTTP 客户端中间件，用于熔断
func HTTPClientMiddleware(cb *CircuitBreaker) func(http.RoundTripper) http.RoundTripper {
	return func(next http.RoundTripper) http.RoundTripper {
		if next == nil {
			next = http.DefaultTransport
		}

		return &circuitBreakerTransport{
			cb:   cb,
			next: next,
		}
	}
}

// circuitBreakerTransport 实现 http.RoundTripper 接口
type circuitBreakerTransport struct {
	cb   *CircuitBreaker
	next http.RoundTripper
}

func (t *circuitBreakerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var resp *http.Response
	err := t.cb.Call(func() error {
		var err error
		resp, err = t.next.RoundTrip(req)
		if err != nil {
			return err
		}

		// 根据状态码判断是否为错误
		if resp.StatusCode >= 500 {
			return &HTTPError{StatusCode: resp.StatusCode}
		}
		return nil
	})

	if err != nil {
		if err == ErrCircuitBreakerOpen || err == ErrTooManyRequests {
			return nil, err
		}
		// 如果是 HTTPError，返回响应和 nil 错误
		if _, ok := err.(*HTTPError); ok {
			return resp, nil
		}
		return nil, err
	}

	return resp, nil
}
