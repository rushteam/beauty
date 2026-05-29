package timeout

import (
	"context"
	"net/http"
)

// HTTPMiddleware 返回一个 HTTP 中间件，用于超时控制。
// 使用标准库 http.TimeoutHandler 实现，避免 goroutine 泄漏和超时后写入已关闭 ResponseWriter 的数据竞争。
func HTTPMiddleware(tc *TimeoutController) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.TimeoutHandler(next, tc.timeout, "Request Timeout")
	}
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

// HTTPTimeoutError 标识 HTTP 响应状态码层面的错误（非超时，用于客户端 Transport）
type HTTPTimeoutError struct {
	StatusCode int
}

func (e *HTTPTimeoutError) Error() string {
	return http.StatusText(e.StatusCode)
}

// IsHTTPTimeoutError 检查错误是否为 HTTP 超时错误
func IsHTTPTimeoutError(err error) bool {
	if err == ErrTimeout || err == ErrTimeoutCanceled {
		return true
	}
	_, ok := err.(*HTTPTimeoutError)
	return ok
}
