package resty

import (
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const defaultTimeout = 30 * time.Second

// NewHTTPClient 返回一个带 OTel trace 传播的标准 *http.Client。
// 默认超时 30s，防止下游无响应时 goroutine 永久挂起。
//
// 用法：
//
//	client := resty.NewHTTPClient()
//	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
//	resp, err := client.Do(req)
//
//	// 自定义超时
//	client := resty.NewHTTPClient(resty.WithTimeout(10 * time.Second))
func NewHTTPClient(opts ...ClientOption) *http.Client {
	cfg := clientConfig{timeout: defaultTimeout}
	for _, o := range opts {
		o(&cfg)
	}
	return &http.Client{
		Timeout:   cfg.timeout,
		Transport: otelhttp.NewTransport(http.DefaultTransport, cfg.otelOpts...),
	}
}

type clientConfig struct {
	timeout  time.Duration
	otelOpts []otelhttp.Option
}

// ClientOption 配置 NewHTTPClient 的选项。
type ClientOption func(*clientConfig)

// WithTimeout 覆盖默认超时时间（默认 30s）。传 0 表示不设超时。
func WithTimeout(d time.Duration) ClientOption {
	return func(c *clientConfig) {
		c.timeout = d
	}
}

// WithOtelOption 透传 otelhttp 选项（如自定义 span name、filter 等）。
func WithOtelOption(opts ...otelhttp.Option) ClientOption {
	return func(c *clientConfig) {
		c.otelOpts = append(c.otelOpts, opts...)
	}
}
