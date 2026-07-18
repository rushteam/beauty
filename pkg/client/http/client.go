package resty

import (
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/rushteam/beauty/pkg/backoff"
	mwcb "github.com/rushteam/beauty/pkg/middleware/circuitbreaker"
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
	// 传输链(外→内):熔断 → 重试 → otel → base。熔断在最外:一次逻辑请求算一个样本,
	// 熔断打开时直接短路、不进重试;otel 在最内:每次实际尝试各自成 span。
	var rt http.RoundTripper = otelhttp.NewTransport(http.DefaultTransport, cfg.otelOpts...)
	if cfg.retry != nil {
		retryable := cfg.retryable
		if retryable == nil {
			retryable = DefaultRetryable
		}
		rt = &retryTransport{base: rt, policy: cfg.retry, retryable: retryable}
	}
	if cfg.breaker != nil {
		rt = mwcb.HTTPClientMiddleware(cfg.breaker)(rt)
	}
	return &http.Client{Timeout: cfg.timeout, Transport: rt}
}

type clientConfig struct {
	timeout   time.Duration
	otelOpts  []otelhttp.Option
	retry     *backoff.Policy
	retryable RetryableFunc
	breaker   *mwcb.CircuitBreaker
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

// WithRetry 开启请求重试:按 backoff.Policy 退避,默认只重试幂等方法的瞬时失败
// (网络错误 / 429 / 502 / 503 / 504),遵守 Retry-After,请求体自动重放。
// 用 backoff.New(backoff.WithMaxRetries(n), ...) 构造 policy。
func WithRetry(policy *backoff.Policy) ClientOption {
	return func(c *clientConfig) { c.retry = policy }
}

// WithRetryable 自定义重试判定(覆盖 DefaultRetryable),需配合 WithRetry。
// 例如让某个已知幂等的 POST 也参与重试。
func WithRetryable(fn RetryableFunc) ClientOption {
	return func(c *clientConfig) { c.retryable = fn }
}

// WithCircuitBreaker 接入请求级熔断(pkg/middleware/circuitbreaker):熔断打开时直接短路,
// 保护下游、快速失败。用 circuitbreaker.NewCircuitBreaker / GetCircuitBreaker 构造。
func WithCircuitBreaker(cb *mwcb.CircuitBreaker) ClientOption {
	return func(c *clientConfig) { c.breaker = cb }
}
