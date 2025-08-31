package timeout

import (
	"context"
	"net/http"
)

// HTTPMiddleware 返回一个 HTTP 中间件，用于超时控制
func HTTPMiddleware(tc *TimeoutController) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			err := tc.Execute(r.Context(), func(ctx context.Context) error {
				// 创建一个新的请求，使用超时上下文
				newReq := r.WithContext(ctx)

				// 创建响应写入器来捕获错误
				rw := &timeoutResponseWriter{ResponseWriter: w}
				next.ServeHTTP(rw, newReq)

				// 如果有错误状态码，返回错误
				if rw.statusCode >= 500 {
					return &HTTPTimeoutError{StatusCode: rw.statusCode}
				}
				return nil
			})

			if err != nil {
				if err == ErrTimeout {
					http.Error(w, "Request Timeout", http.StatusRequestTimeout)
					return
				}
				if err == ErrTimeoutCanceled {
					http.Error(w, "Request Canceled", http.StatusRequestTimeout)
					return
				}
				// 如果是 HTTPTimeoutError，说明已经处理过了
				if _, ok := err.(*HTTPTimeoutError); ok {
					return
				}
				// 其他错误
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		})
	}
}

// HTTPTimeoutError HTTP 超时错误类型
type HTTPTimeoutError struct {
	StatusCode int
}

func (e *HTTPTimeoutError) Error() string {
	return http.StatusText(e.StatusCode)
}

// timeoutResponseWriter 包装 http.ResponseWriter 以捕获状态码
type timeoutResponseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (rw *timeoutResponseWriter) WriteHeader(code int) {
	if !rw.written {
		rw.statusCode = code
		rw.written = true
		rw.ResponseWriter.WriteHeader(code)
	}
}

func (rw *timeoutResponseWriter) Write(data []byte) (int, error) {
	if !rw.written {
		rw.WriteHeader(http.StatusOK)
	}
	return rw.ResponseWriter.Write(data)
}

// HTTPClientMiddleware 返回一个 HTTP 客户端中间件，用于超时控制
func HTTPClientMiddleware(tc *TimeoutController) func(http.RoundTripper) http.RoundTripper {
	return func(next http.RoundTripper) http.RoundTripper {
		if next == nil {
			next = http.DefaultTransport
		}

		return &timeoutTransport{
			tc:   tc,
			next: next,
		}
	}
}

// timeoutTransport 实现 http.RoundTripper 接口
type timeoutTransport struct {
	tc   *TimeoutController
	next http.RoundTripper
}

func (t *timeoutTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var resp *http.Response
	err := t.tc.Execute(req.Context(), func(ctx context.Context) error {
		var err error
		// 使用超时上下文创建新请求
		newReq := req.WithContext(ctx)
		resp, err = t.next.RoundTrip(newReq)
		if err != nil {
			return err
		}

		// 根据状态码判断是否为错误
		if resp.StatusCode >= 500 {
			return &HTTPTimeoutError{StatusCode: resp.StatusCode}
		}
		return nil
	})

	if err != nil {
		if err == ErrTimeout || err == ErrTimeoutCanceled {
			return nil, err
		}
		// 如果是 HTTPTimeoutError，返回响应和 nil 错误
		if _, ok := err.(*HTTPTimeoutError); ok {
			return resp, nil
		}
		return nil, err
	}

	return resp, nil
}

// IsHTTPTimeoutError 检查错误是否为 HTTP 超时错误
func IsHTTPTimeoutError(err error) bool {
	if err == ErrTimeout || err == ErrTimeoutCanceled {
		return true
	}
	_, ok := err.(*HTTPTimeoutError)
	return ok
}
