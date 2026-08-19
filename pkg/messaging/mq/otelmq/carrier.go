package otelmq

// headersCarrier 把 mq.Message.Headers 适配为 OTel TextMapCarrier,
// 与 gRPC 的 otelgrpc / franz kotel 一样走 W3C TraceContext(traceparent/tracestate)。
type headersCarrier map[string]string

func (c headersCarrier) Get(key string) string { return c[key] }

func (c headersCarrier) Set(key, val string) { c[key] = val }

func (c headersCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}

// cloneHeaders 复制 headers,避免装饰器/中间件改写调用方 map。
func cloneHeaders(in map[string]string) map[string]string {
	out := make(map[string]string, len(in)+4)
	for k, v := range in {
		out[k] = v
	}
	return out
}
