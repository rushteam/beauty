package auth

import (
	"context"
	"errors"
	"net/http"
)

// HTTPMiddleware 返回 HTTP 认证中间件
func HTTPMiddleware(auth *AuthMiddleware) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 检查是否跳过认证
			if auth.ShouldSkip(r.URL.Path) {
				auth.recordSkipped()
				next.ServeHTTP(w, r)
				return
			}

			// 构建元数据
			metadata := buildHTTPMetadata(r)

			// 执行认证
			user, err := auth.Authenticate(r.Context(), metadata)
			if err != nil {
				handleAuthError(w, err)
				return
			}

			// 执行授权（如果配置了授权器）
			if err := auth.Authorize(r.Context(), user, r.URL.Path, r.Method); err != nil {
				handleAuthError(w, err)
				return
			}

			// 将用户信息添加到上下文
			ctx := context.WithValue(r.Context(), userContextKey, user)
			r = r.WithContext(ctx)

			// 调用成功回调
			if auth.onAuthSuccess != nil {
				auth.onAuthSuccess(r.Context(), user)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// buildHTTPMetadata 构建 HTTP 请求元数据。
// headers/query 直接引用请求原始对象，避免拷贝；cookies 因需要 name→value 映射仍需构建。
func buildHTTPMetadata(r *http.Request) map[string]any {
	md := make(map[string]any, 7)

	md["headers"] = r.Header      // http.Header 即 map[string][]string，直接引用
	md["query"] = r.URL.RawQuery  // 原始 query string，避免 ParseQuery 的 map 分配
	md["method"] = r.Method
	md["path"] = r.URL.Path
	md["remote_addr"] = r.RemoteAddr
	md["user_agent"] = r.UserAgent()

	// cookies 需要 name→value 映射，无法直接复用
	cookies := r.Cookies()
	if len(cookies) > 0 {
		cookieMap := make(map[string]string, len(cookies))
		for _, cookie := range cookies {
			cookieMap[cookie.Name] = cookie.Value
		}
		md["cookies"] = cookieMap
	}

	return md
}

// handleAuthError 处理认证错误
func handleAuthError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrInvalidToken) || errors.Is(err, ErrTokenExpired) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	} else if errors.Is(err, ErrForbidden) {
		http.Error(w, "Forbidden", http.StatusForbidden)
	} else {
		http.Error(w, "Authentication Error", http.StatusUnauthorized)
	}
}

// GetUserFromContext 从上下文中获取用户信息
func GetUserFromContext(ctx context.Context) (User, bool) {
	user, ok := ctx.Value(userContextKey).(User)
	return user, ok
}

// RequireAuth 创建需要认证的中间件（用于特定路由）
func RequireAuth(auth *AuthMiddleware) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			metadata := buildHTTPMetadata(r)

			user, err := auth.Authenticate(r.Context(), metadata)
			if err != nil {
				handleAuthError(w, err)
				return
			}

			ctx := context.WithValue(r.Context(), userContextKey, user)
			r = r.WithContext(ctx)

			if auth.onAuthSuccess != nil {
				auth.onAuthSuccess(r.Context(), user)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequireRole 创建需要特定角色的中间件
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := GetUserFromContext(r.Context())
			if !ok {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// 检查用户是否拥有任一所需角色
			userRoles := user.Roles()
			hasRole := false
			for _, requiredRole := range roles {
				for _, userRole := range userRoles {
					if userRole == requiredRole {
						hasRole = true
						break
					}
				}
				if hasRole {
					break
				}
			}

			if !hasRole {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// OptionalAuth 创建可选认证中间件（认证失败不会阻止请求）
func OptionalAuth(auth *AuthMiddleware) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			metadata := buildHTTPMetadata(r)

			user, err := auth.Authenticate(r.Context(), metadata)
			if err == nil {
				// 认证成功，将用户信息添加到上下文
				ctx := context.WithValue(r.Context(), userContextKey, user)
				r = r.WithContext(ctx)

				if auth.onAuthSuccess != nil {
					auth.onAuthSuccess(r.Context(), user)
				}
			}
			// 认证失败也继续处理请求，不设置用户信息

			next.ServeHTTP(w, r)
		})
	}
}
