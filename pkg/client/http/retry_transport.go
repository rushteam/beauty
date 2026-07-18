package resty

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/rushteam/beauty/pkg/backoff"
)

// RetryableFunc 判断一次结果是否值得重试。resp 在网络错误时为 nil。
type RetryableFunc func(req *http.Request, resp *http.Response, err error) bool

// DefaultRetryable 是默认重试判定:只重试**幂等**方法(GET/HEAD/OPTIONS/TRACE/PUT/DELETE)——
//   - 网络/传输错误(err != nil);
//   - 响应 429 / 502 / 503 / 504。
//
// 非幂等方法(POST/PATCH 等)默认不重试(可能已在服务端产生副作用);要重试请自定义 RetryableFunc。
func DefaultRetryable(req *http.Request, resp *http.Response, err error) bool {
	if !idempotent(req.Method) {
		return false
	}
	if err != nil {
		return true
	}
	switch resp.StatusCode {
	case http.StatusTooManyRequests, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

func idempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions,
		http.MethodTrace, http.MethodPut, http.MethodDelete:
		return true
	}
	return false
}

// retryTransport 用 backoff.Policy 对瞬时失败重试的 http.RoundTripper。请求体会被缓冲以便重放。
type retryTransport struct {
	base      http.RoundTripper
	policy    *backoff.Policy
	retryable RetryableFunc
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// 准备请求体重放:优先 GetBody,否则一次性读入内存并补上 GetBody。
	getBody := req.GetBody
	if getBody == nil && req.Body != nil && req.Body != http.NoBody {
		buf, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
		getBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(buf)), nil }
		req.Body, _ = getBody()
	}

	attempts := max(t.policy.MaxRetries()+1, 1)
	var (
		resp *http.Response
		err  error
	)
	for i := range attempts {
		if i > 0 {
			// 重放请求体。
			if getBody != nil {
				body, gerr := getBody()
				if gerr != nil {
					return nil, gerr
				}
				req.Body = body
			}
			// 等待:退避时长与 Retry-After 取较大者。
			wait := t.policy.Duration(i)
			if resp != nil {
				if ra := retryAfter(resp); ra > wait {
					wait = ra
				}
			}
			// 丢弃上一轮响应体,腾出连接复用。
			if resp != nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				resp = nil
			}
			select {
			case <-time.After(wait):
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
		}

		resp, err = t.base.RoundTrip(req)
		if !t.retryable(req, resp, err) {
			return resp, err // 成功或不可重试:直接返回
		}
		if i == attempts-1 {
			return resp, err // 最后一次:返回当前结果(交给调用方处理)
		}
	}
	return resp, err
}

// retryAfter 解析 Retry-After(仅支持秒数形式;HTTP-date 形式忽略,返回 0)。
func retryAfter(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	return 0
}
