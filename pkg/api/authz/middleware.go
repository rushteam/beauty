package authz

import (
	"context"
	"net/http"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RequestMapper 从 HTTP 请求推出被授权的 (action, resource)。这是 policy——按你的路由/资源规范定。
type RequestMapper func(*http.Request) (action, resource string)

// MethodResourceMapper 是默认映射:action 由 HTTP 方法推(见 MethodAction),resource 取请求路径。
func MethodResourceMapper(r *http.Request) (string, string) {
	return MethodAction(r.Method), r.URL.Path
}

// MethodAction 把 HTTP 方法映射成动作:GET/HEAD→read,POST→create,PUT/PATCH→update,
// DELETE→delete,其它→方法名小写。
func MethodAction(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead:
		return "read"
	case http.MethodPost:
		return "create"
	case http.MethodPut, http.MethodPatch:
		return "update"
	case http.MethodDelete:
		return "delete"
	default:
		return strings.ToLower(method)
	}
}

// HTTP 返回一个授权中间件:从 context 取 Subject(认证层填),按 mapper 推 (action, resource),
// 调 e.Authorize。无主体→401;拒绝→403;放行→next。mapper 为 nil 时用 MethodResourceMapper。
func HTTP(e Enforcer, mapper RequestMapper) func(http.Handler) http.Handler {
	if mapper == nil {
		mapper = MethodResourceMapper
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sub, ok := SubjectFromContext(r.Context())
			if !ok {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			action, resource := mapper(r)
			if err := e.Authorize(r.Context(), sub, action, resource); err != nil {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// MethodMapper 从 gRPC 全方法名(/pkg.Svc/Method)推出 (action, resource)。policy。
type MethodMapper func(fullMethod string) (action, resource string)

// defaultMethodMapper:resource=服务名,action=方法名(如 /user.User/Delete → action=Delete, resource=user.User)。
func defaultMethodMapper(full string) (string, string) {
	full = strings.TrimPrefix(full, "/")
	svc, method, ok := strings.Cut(full, "/")
	if !ok {
		return full, ""
	}
	return method, svc
}

// UnaryServerInterceptor 返回一个 gRPC 一元拦截器:从 context 取 Subject,按 mapper 推
// (action, resource),调 e.Authorize。无主体→Unauthenticated;拒绝→PermissionDenied。
func UnaryServerInterceptor(e Enforcer, mapper MethodMapper) grpc.UnaryServerInterceptor {
	if mapper == nil {
		mapper = defaultMethodMapper
	}
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		sub, ok := SubjectFromContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "authz: no subject")
		}
		action, resource := mapper(info.FullMethod)
		if err := e.Authorize(ctx, sub, action, resource); err != nil {
			return nil, status.Error(codes.PermissionDenied, "authz: denied")
		}
		return handler(ctx, req)
	}
}
