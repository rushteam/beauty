package requestid

import (
	"context"
	"net/http"

	"github.com/rushteam/beauty/pkg/foundation/ctxkey"
	"github.com/rushteam/beauty/pkg/utils/uuid"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const Header = "X-Request-ID"
const metaKey = "x-request-id"

var reqIDKey = ctxkey.New[string]()

// IDGenerator 根据当前 context 生成请求 ID。
type IDGenerator func(ctx context.Context) string

// Option 配置 requestid 中间件的可选参数。
type Option func(*config)

type config struct {
	generator IDGenerator
}

func newConfig(opts []Option) config {
	cfg := config{generator: defaultGenerator}
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

// WithIDGenerator 自定义 ID 生成函数，替代默认的 trace_id > UUIDv7 策略。
func WithIDGenerator(gen IDGenerator) Option {
	return func(c *config) { c.generator = gen }
}

// defaultGenerator 优先复用 OTel trace ID，不存在有效 span 时 fallback 到 UUIDv7。
func defaultGenerator(ctx context.Context) string {
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().IsValid() {
		return span.SpanContext().TraceID().String()
	}
	return uuid.New()
}

// FromContext extracts the request ID from ctx.
func FromContext(ctx context.Context) string {
	id, _ := ctxkey.Get(ctx, reqIDKey)
	return id
}

// NewContext returns a copy of ctx with the given request ID attached.
func NewContext(ctx context.Context, id string) context.Context {
	return ctxkey.With(ctx, reqIDKey, id)
}

// HTTPMiddleware 注入或透传 X-Request-ID。
// 优先级：上游 Header > OTel trace ID > UUIDv7。
// ID 会设置到响应 Header 并注入 request context。
func HTTPMiddleware(next http.Handler) http.Handler {
	return NewHTTPMiddleware()(next)
}

// NewHTTPMiddleware 创建支持自定义选项的 requestid HTTP 中间件。
//
//	webserver.New(addr, mux, webserver.WithMiddleware(
//	    requestid.NewHTTPMiddleware(requestid.WithIDGenerator(myGen)),
//	))
func NewHTTPMiddleware(opts ...Option) func(http.Handler) http.Handler {
	cfg := newConfig(opts)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(Header)
			if id == "" {
				id = cfg.generator(r.Context())
			}
			w.Header().Set(Header, id)
			next.ServeHTTP(w, r.WithContext(NewContext(r.Context(), id)))
		})
	}
}

// UnaryServerInterceptor 为 gRPC unary 调用透传或生成 x-request-id。
func UnaryServerInterceptor(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	ctx = injectFromGRPCMeta(ctx, defaultGenerator)
	return handler(ctx, req)
}

// StreamServerInterceptor 为 gRPC streaming 调用透传或生成 x-request-id。
func StreamServerInterceptor(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	ctx := injectFromGRPCMeta(ss.Context(), defaultGenerator)
	return handler(srv, &wrappedStream{ss, ctx})
}

// NewUnaryServerInterceptor 创建支持自定义选项的 gRPC unary 拦截器。
func NewUnaryServerInterceptor(opts ...Option) grpc.UnaryServerInterceptor {
	cfg := newConfig(opts)
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx = injectFromGRPCMeta(ctx, cfg.generator)
		return handler(ctx, req)
	}
}

// NewStreamServerInterceptor 创建支持自定义选项的 gRPC stream 拦截器。
func NewStreamServerInterceptor(opts ...Option) grpc.StreamServerInterceptor {
	cfg := newConfig(opts)
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := injectFromGRPCMeta(ss.Context(), cfg.generator)
		return handler(srv, &wrappedStream{ss, ctx})
	}
}

func injectFromGRPCMeta(ctx context.Context, gen IDGenerator) context.Context {
	id := ""
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get(metaKey); len(vals) > 0 {
			id = vals[0]
		}
	}
	if id == "" {
		id = gen(ctx)
	}
	return NewContext(ctx, id)
}

type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedStream) Context() context.Context { return w.ctx }
