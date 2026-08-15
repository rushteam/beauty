// Package auth 提供 GraphQL 层的认证提取与下游透传。
// 从 HTTP 请求中提取认证信息注入 gqlgen context,resolver 中通过 context 取出后
// 自动透传到 gRPC metadata / HTTP header。
package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/99designs/gqlgen/graphql"
)

type contextKey struct{ name string }

var (
	tokenKey = &contextKey{"graphql-auth-token"}
	userKey  = &contextKey{"graphql-auth-user"}
)

// UserInfo 是从认证中提取的用户信息。
type UserInfo struct {
	ID       string
	Username string
	Token    string
	Metadata map[string]string
}

// GetUser 从 context 中获取认证用户信息。
func GetUser(ctx context.Context) (UserInfo, bool) {
	u, ok := ctx.Value(userKey).(UserInfo)
	return u, ok
}

// GetToken 从 context 中获取原始 token。
func GetToken(ctx context.Context) string {
	t, _ := ctx.Value(tokenKey).(string)
	return t
}

// WithUser 将用户信息注入 context。
func WithUser(ctx context.Context, user UserInfo) context.Context {
	return context.WithValue(ctx, userKey, user)
}

// WithToken 将原始 token 注入 context。
func WithToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, tokenKey, token)
}

// TokenExtractor 从 HTTP 请求中提取 token。
type TokenExtractor func(r *http.Request) string

// BearerExtractor 从 Authorization: Bearer <token> 中提取。
func BearerExtractor() TokenExtractor {
	return func(r *http.Request) string {
		auth := r.Header.Get("Authorization")
		if strings.HasPrefix(auth, "Bearer ") {
			return strings.TrimPrefix(auth, "Bearer ")
		}
		return ""
	}
}

// HeaderExtractor 从指定 header 提取 token。
func HeaderExtractor(header string) TokenExtractor {
	return func(r *http.Request) string {
		return r.Header.Get(header)
	}
}

// QueryExtractor 从 URL query 参数提取 token。
func QueryExtractor(param string) TokenExtractor {
	return func(r *http.Request) string {
		return r.URL.Query().Get(param)
	}
}

// Authenticator 验证 token 并返回用户信息。
type Authenticator func(ctx context.Context, token string) (UserInfo, error)

// HTTPMiddleware 创建 HTTP 中间件,从请求中提取 token 并验证,注入 context。
// 验证失败不阻断请求(GraphQL 通常在 resolver 层处理权限),仅将 token/user 注入 ctx。
func HTTPMiddleware(extractor TokenExtractor, auth Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractor(r)
			if token == "" {
				next.ServeHTTP(w, r)
				return
			}
			ctx := WithToken(r.Context(), token)
			if auth != nil {
				user, err := auth(ctx, token)
				if err == nil {
					ctx = WithUser(ctx, user)
				}
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// PropagateAuth 返回 gqlgen OperationMiddleware,确保认证信息从请求级 context
// 流到 operation context(默认已透传,本函数供需要额外处理的场景)。
func PropagateAuth() graphql.OperationMiddleware {
	return func(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
		return next(ctx)
	}
}

// OutgoingMetadata 从 context 中提取认证信息,返回适合注入 gRPC metadata 或
// HTTP header 的键值对。供 resolver 中调下游服务时使用。
func OutgoingMetadata(ctx context.Context) map[string]string {
	meta := make(map[string]string)
	if token := GetToken(ctx); token != "" {
		meta["authorization"] = "Bearer " + token
	}
	if user, ok := GetUser(ctx); ok {
		if user.ID != "" {
			meta["x-user-id"] = user.ID
		}
		if user.Username != "" {
			meta["x-username"] = user.Username
		}
		for k, v := range user.Metadata {
			meta[k] = v
		}
	}
	return meta
}
