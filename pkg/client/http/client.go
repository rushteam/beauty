package resty

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// NewHTTPClient 返回一个带 OTel trace 传播的标准 *http.Client。
// 每次请求会自动从 ctx 中提取当前 span 并将 traceparent / tracestate 注入出站 Header，
// 确保 A→B 的 HTTP 调用链路不断开。
//
// 用法：
//
//	client := resty.NewHTTPClient()
//	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
//	resp, err := client.Do(req)
func NewHTTPClient(opts ...otelhttp.Option) *http.Client {
	return &http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport, opts...),
	}
}
