package metrics

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

var histogramBoundaries = []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000}

type httpMetrics struct {
	reqCount    metric.Int64Counter
	reqDuration metric.Float64Histogram
	reqInflight metric.Int64UpDownCounter
}

type grpcMetrics struct {
	reqCount    metric.Int64Counter
	reqDuration metric.Float64Histogram
	reqInflight metric.Int64UpDownCounter
}

var (
	httpOnce  sync.Once
	httpM     *httpMetrics
	httpMErr  error
	grpcOnce  sync.Once
	grpcM     *grpcMetrics
	grpcMErr  error
)

func initHTTPMetrics(serviceName string) (*httpMetrics, error) {
	httpOnce.Do(func() {
		m := otel.GetMeterProvider().Meter(serviceName)
		bOpt := metric.WithExplicitBucketBoundaries(histogramBoundaries...)

		count, err := m.Int64Counter(
			"http.server.request.count",
			metric.WithDescription("Total number of HTTP requests"),
		)
		if err != nil {
			httpMErr = err
			return
		}
		dur, err := m.Float64Histogram(
			"http.server.request.duration",
			metric.WithDescription("HTTP request duration in milliseconds"),
			metric.WithUnit("ms"),
			bOpt,
		)
		if err != nil {
			httpMErr = err
			return
		}
		inflight, err := m.Int64UpDownCounter(
			"http.server.request.inflight",
			metric.WithDescription("Number of in-flight HTTP requests"),
		)
		if err != nil {
			httpMErr = err
			return
		}
		httpM = &httpMetrics{
			reqCount:    count,
			reqDuration: dur,
			reqInflight: inflight,
		}
	})
	return httpM, httpMErr
}

func initGRPCMetrics(serviceName string) (*grpcMetrics, error) {
	grpcOnce.Do(func() {
		m := otel.GetMeterProvider().Meter(serviceName)
		bOpt := metric.WithExplicitBucketBoundaries(histogramBoundaries...)

		count, err := m.Int64Counter(
			"grpc.server.request.count",
			metric.WithDescription("Total number of gRPC requests"),
		)
		if err != nil {
			grpcMErr = err
			return
		}
		dur, err := m.Float64Histogram(
			"grpc.server.request.duration",
			metric.WithDescription("gRPC request duration in milliseconds"),
			metric.WithUnit("ms"),
			bOpt,
		)
		if err != nil {
			grpcMErr = err
			return
		}
		inflight, err := m.Int64UpDownCounter(
			"grpc.server.request.inflight",
			metric.WithDescription("Number of in-flight gRPC requests"),
		)
		if err != nil {
			grpcMErr = err
			return
		}
		grpcM = &grpcMetrics{
			reqCount:    count,
			reqDuration: dur,
			reqInflight: inflight,
		}
	})
	return grpcM, grpcMErr
}

// httpConfig 是 HTTPMiddleware 的可选配置。
type httpConfig struct {
	routeExtractor func(*http.Request) string
}

// Option 配置 HTTPMiddleware。
type Option func(*httpConfig)

// WithRouteExtractor 自定义如何从请求中取「路由模板」（用作指标 route 标签）。
//
// 框架不绑定具体 router，默认能直接识别标准库 http.ServeMux（Go 1.22+ 的 r.Pattern）。
// 若用 chi / gin 等第三方 router，可在此注入对应取值方式，避免把真实路径（/users/123）
// 当成标签导致 Prometheus 时间序列基数爆炸：
//
//	// chi
//	metrics.WithRouteExtractor(func(r *http.Request) string {
//		return chi.RouteContext(r.Context()).RoutePattern()
//	})
//
//	// gin（在 gin 自己的中间件里拿 c.FullPath() 更直接，此处仅示意）
//
// 返回空字符串表示「未匹配」，会回退到启发式模板化。
func WithRouteExtractor(fn func(*http.Request) string) Option {
	return func(c *httpConfig) {
		c.routeExtractor = fn
	}
}

// resolveRoute 解析用作 route 标签的低基数路由模板，优先级：
// 自定义 extractor → 标准库 r.Pattern → 启发式模板化（兜底，防止基数爆炸）。
func resolveRoute(r *http.Request, extractor func(*http.Request) string) string {
	if extractor != nil {
		if route := extractor(r); route != "" {
			return route
		}
	}
	// 标准库 ServeMux（Go 1.22+）路由后会在 r.Pattern 写入匹配到的模式，
	// 形如 "GET /users/{id}" 或 "example.com/users/{id}"。
	// 本中间件在 next.ServeHTTP 返回后才记录，此时 Pattern 已被填充。
	if r.Pattern != "" {
		return patternPath(r.Pattern)
	}
	// 未知 router 或未匹配到路由：对真实路径做启发式模板化，约束基数。
	return templatePath(r.URL.Path)
}

// patternPath 从 r.Pattern 中剥离可选的 "METHOD " 前缀和 HOST 前缀，只留路径模板。
func patternPath(pattern string) string {
	if _, after, found := strings.Cut(pattern, " "); found { // 去掉 "METHOD "
		pattern = after
	}
	if i := strings.IndexByte(pattern, '/'); i > 0 { // 去掉 HOST（路径以 '/' 开头）
		pattern = pattern[i:]
	}
	return pattern
}

const (
	maxRouteSegments = 12  // 超出视为异常路径，统一归并
	maxRouteLen      = 100 // 模板化后仍过长则归并，防御性兜底
)

// templatePath 将真实路径中「像 ID 的段」替换为占位符（/users/123 → /users/{id}），
// 在拿不到精确路由模板时把标签基数收敛到有限集合。
func templatePath(path string) string {
	if path == "" {
		return "/"
	}
	segs := strings.Split(path, "/")
	if len(segs) > maxRouteSegments {
		return "/other"
	}
	for i, s := range segs {
		if ph := placeholder(s); ph != "" {
			segs[i] = ph
		}
	}
	out := strings.Join(segs, "/")
	if len(out) > maxRouteLen {
		return "/other"
	}
	return out
}

// placeholder 判断一个路径段是否「像变量」，是则返回占位符，否则返回空串。
func placeholder(s string) string {
	if s == "" {
		return ""
	}
	switch {
	case isAllDigits(s):
		return "{id}"
	case isUUID(s):
		return "{uuid}"
	case len(s) >= 32 && isHex(s):
		return "{hash}"
	case len(s) >= 21 && hasDigit(s): // base62/snowflake 之类的长 ID
		return "{id}"
	}
	return ""
}

func isAllDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func hasDigit(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			return true
		}
	}
	return false
}

func isHexByte(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isHexByte(s[i]) {
			return false
		}
	}
	return true
}

// isUUID 判断是否为 8-4-4-4-12 的十六进制 UUID。
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if s[i] != '-' {
				return false
			}
			continue
		}
		if !isHexByte(s[i]) {
			return false
		}
	}
	return true
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func HTTPMiddleware(serviceName string, opts ...Option) func(http.Handler) http.Handler {
	cfg := &httpConfig{}
	for _, o := range opts {
		o(cfg)
	}
	return func(next http.Handler) http.Handler {
		m, err := initHTTPMetrics(serviceName)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err != nil || m == nil {
				next.ServeHTTP(w, r)
				return
			}

			method := r.Method
			ctx := r.Context()

			inflightAttrs := metric.WithAttributes(
				attribute.String("method", method),
			)
			m.reqInflight.Add(ctx, 1, inflightAttrs)
			defer m.reqInflight.Add(ctx, -1, inflightAttrs)

			rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(rw, r)
			elapsed := float64(time.Since(start).Milliseconds())

			// 在 next 返回后解析路由模板：此时标准库 ServeMux 已填充 r.Pattern，
			// 用低基数的路由模板（/users/{id}）而非真实路径（/users/123）做标签。
			route := resolveRoute(r, cfg.routeExtractor)

			attrs := metric.WithAttributes(
				attribute.String("method", method),
				attribute.String("route", route),
				attribute.String("status_code", fmt.Sprintf("%d", rw.statusCode)),
			)
			m.reqCount.Add(ctx, 1, attrs)
			m.reqDuration.Record(ctx, elapsed, attrs)
		})
	}
}

func UnaryServerInterceptor(serviceName string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		m, err := initGRPCMetrics(serviceName)
		if err != nil || m == nil {
			return handler(ctx, req)
		}

		method := info.FullMethod
		inflightAttrs := metric.WithAttributes(
			attribute.String("method", method),
		)
		m.reqInflight.Add(ctx, 1, inflightAttrs)
		defer m.reqInflight.Add(ctx, -1, inflightAttrs)

		start := time.Now()
		resp, handlerErr := handler(ctx, req)
		elapsed := float64(time.Since(start).Milliseconds())

		code := status.Code(handlerErr).String()
		attrs := metric.WithAttributes(
			attribute.String("method", method),
			attribute.String("status_code", code),
		)
		m.reqCount.Add(ctx, 1, attrs)
		m.reqDuration.Record(ctx, elapsed, attrs)

		return resp, handlerErr
	}
}

func StreamServerInterceptor(serviceName string) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		m, err := initGRPCMetrics(serviceName)
		if err != nil || m == nil {
			return handler(srv, ss)
		}

		ctx := ss.Context()
		method := info.FullMethod
		inflightAttrs := metric.WithAttributes(
			attribute.String("method", method),
		)
		m.reqInflight.Add(ctx, 1, inflightAttrs)
		defer m.reqInflight.Add(ctx, -1, inflightAttrs)

		start := time.Now()
		handlerErr := handler(srv, ss)
		elapsed := float64(time.Since(start).Milliseconds())

		code := status.Code(handlerErr).String()
		attrs := metric.WithAttributes(
			attribute.String("method", method),
			attribute.String("status_code", code),
		)
		m.reqCount.Add(ctx, 1, attrs)
		m.reqDuration.Record(ctx, elapsed, attrs)

		return handlerErr
	}
}
