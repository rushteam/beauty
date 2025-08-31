package auth

import (
	"context"
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
			ctx := context.WithValue(r.Context(), "user", user)
			r = r.WithContext(ctx)

			// 调用成功回调
			if auth.onAuthSuccess != nil {
				auth.onAuthSuccess(r.Context(), user)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// buildHTTPMetadata 构建 HTTP 请求元数据
func buildHTTPMetadata(r *http.Request) map[string]interface{} {
	metadata := make(map[string]interface{})

	// 添加 headers
	headers := make(map[string][]string)
	for name, values := range r.Header {
		headers[name] = values
	}
	metadata["headers"] = headers

	// 添加查询参数
	metadata["query"] = r.URL.Query()

	// 添加 cookies
	cookies := make(map[string]string)
	for _, cookie := range r.Cookies() {
		cookies[cookie.Name] = cookie.Value
	}
	metadata["cookies"] = cookies

	// 添加其他信息
	metadata["method"] = r.Method
	metadata["path"] = r.URL.Path
	metadata["remote_addr"] = r.RemoteAddr
	metadata["user_agent"] = r.UserAgent()

	return metadata
}

// handleAuthError 处理认证错误
func handleAuthError(w http.ResponseWriter, err error) {
	if err == ErrUnauthorized || err == ErrInvalidToken || err == ErrTokenExpired {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	} else if err == ErrForbidden {
		http.Error(w, "Forbidden", http.StatusForbidden)
	} else {
		http.Error(w, "Authentication Error", http.StatusUnauthorized)
	}
}

// GetUserFromContext 从上下文中获取用户信息
func GetUserFromContext(ctx context.Context) (User, bool) {
	user, ok := ctx.Value("user").(User)
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

			ctx := context.WithValue(r.Context(), "user", user)
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
				ctx := context.WithValue(r.Context(), "user", user)
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
