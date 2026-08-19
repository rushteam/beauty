// Package tenant 提供多租户隔离中间件。
//
// 从请求中提取租户 ID（HTTP Header / gRPC metadata / context 中的 metadata.MD），
// 校验其存在性（可选白名单校验），并注入独立的 typed context key 供下游读取。
//
// 典型用法：
//
//	webserver.WithMiddleware(
//	    propagation.HTTPServerMiddleware,    // 可选：先提取所有 x-* 到 MD
//	    tenant.HTTPMiddleware(),             // 校验 + 注入 tenant ID
//	    ratelimit.HTTPMiddleware(rl),        // per-tenant 限流
//	)
//
// 业务代码读取：
//
//	tenantID := tenant.FromContext(ctx)
package tenant

import (
	"context"
	"net/http"
	"strings"

	"github.com/rushteam/beauty/pkg/api/metadata"
	"github.com/rushteam/beauty/pkg/foundation/ctxkey"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcmd "google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var tenantKey = ctxkey.New[string]()

// FromContext 返回当前请求的租户 ID；未注入时返回空字符串。
func FromContext(ctx context.Context) string {
	id, _ := ctxkey.Get(ctx, tenantKey)
	return id
}

// NewContext 将租户 ID 注入 context。一般由中间件自动调用，业务代码通常无需手动使用。
func NewContext(ctx context.Context, tenantID string) context.Context {
	return ctxkey.With(ctx, tenantKey, tenantID)
}

// Option 配置 tenant 中间件。
type Option func(*config)

type config struct {
	headerName   string
	required     bool
	validator    func(string) bool
	errorHandler func(w http.ResponseWriter, r *http.Request)
}

func defaultConfig() config {
	return config{
		headerName: "X-Tenant-ID",
		required:   true,
	}
}

// WithHeaderName 自定义租户 ID 的 Header 名称，默认 "X-Tenant-ID"。
func WithHeaderName(name string) Option {
	return func(c *config) { c.headerName = name }
}

// WithRequired 设置是否强制要求租户 ID 存在，默认 true。
// false 时缺少租户 ID 不拦截，仅不注入 context（FromContext 返回空字符串）。
func WithRequired(required bool) Option {
	return func(c *config) { c.required = required }
}

// WithValidator 设置租户 ID 校验函数。返回 false 视为非法租户，拒绝请求。
func WithValidator(fn func(tenantID string) bool) Option {
	return func(c *config) { c.validator = fn }
}

// WithErrorHandler 自定义 HTTP 拒绝时的响应逻辑。
// 默认返回 403 Forbidden + 纯文本错误信息。
func WithErrorHandler(fn func(w http.ResponseWriter, r *http.Request)) Option {
	return func(c *config) { c.errorHandler = fn }
}

// HTTPMiddleware 返回 HTTP 租户校验中间件。
// 提取顺序：metadata.MD (context) → Header → 拒绝/跳过。
func HTTPMiddleware(opts ...Option) func(http.Handler) http.Handler {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tenantID := extractHTTP(r, cfg.headerName)
			ctx, ok := validate(r.Context(), tenantID, cfg)
			if !ok {
				if cfg.errorHandler != nil {
					cfg.errorHandler(w, r)
				} else {
					http.Error(w, "Forbidden: missing or invalid tenant ID", http.StatusForbidden)
				}
				return
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// UnaryServerInterceptor 返回 gRPC unary 租户校验拦截器。
func UnaryServerInterceptor(opts ...Option) grpc.UnaryServerInterceptor {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		tenantID := extractGRPC(ctx, cfg.headerName)
		ctx, ok := validate(ctx, tenantID, cfg)
		if !ok {
			return nil, status.Error(codes.PermissionDenied, "missing or invalid tenant ID")
		}
		return handler(ctx, req)
	}
}

// StreamServerInterceptor 返回 gRPC stream 租户校验拦截器。
func StreamServerInterceptor(opts ...Option) grpc.StreamServerInterceptor {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := ss.Context()
		tenantID := extractGRPC(ctx, cfg.headerName)
		ctx, ok := validate(ctx, tenantID, cfg)
		if !ok {
			return status.Error(codes.PermissionDenied, "missing or invalid tenant ID")
		}
		return handler(srv, &wrappedStream{ss, ctx})
	}
}

// extractHTTP 从 HTTP 请求提取租户 ID：先查 metadata.MD，再查 Header。
func extractHTTP(r *http.Request, headerName string) string {
	if id := metadata.FromContext(r.Context()).Get(metadata.KeyTenantID); id != "" {
		return id
	}
	return r.Header.Get(headerName)
}

// extractGRPC 从 gRPC context 提取租户 ID：先查 metadata.MD，再查 incoming metadata。
func extractGRPC(ctx context.Context, headerName string) string {
	if id := metadata.FromContext(ctx).Get(metadata.KeyTenantID); id != "" {
		return id
	}
	if md, ok := grpcmd.FromIncomingContext(ctx); ok {
		key := strings.ToLower(headerName)
		if vals := md.Get(key); len(vals) > 0 {
			return vals[0]
		}
	}
	return ""
}

// validate 校验租户 ID 并注入 context。返回 (ctx, true) 表示通过。
func validate(ctx context.Context, tenantID string, cfg config) (context.Context, bool) {
	if tenantID == "" {
		if cfg.required {
			return ctx, false
		}
		return ctx, true
	}
	if cfg.validator != nil && !cfg.validator(tenantID) {
		return ctx, false
	}
	return NewContext(ctx, tenantID), true
}

type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedStream) Context() context.Context { return w.ctx }
