package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/auth"
	"github.com/rushteam/beauty/pkg/circuitbreaker"
	"github.com/rushteam/beauty/pkg/logger"
	"github.com/rushteam/beauty/pkg/ratelimit"
	"github.com/rushteam/beauty/pkg/service/grpcserver"
	"github.com/rushteam/beauty/pkg/service/webserver"
	"github.com/rushteam/beauty/pkg/timeout"
	"google.golang.org/grpc"
)

func main() {
	// 创建认证器
	authenticator := auth.NewStaticTokenAuthenticator()
	// 添加一些测试用户
	authenticator.AddToken("admin-token", auth.NewUser("1", "admin", []string{"admin", "user"}))
	authenticator.AddToken("user-token", auth.NewUser("2", "user", []string{"user"}))
	authenticator.AddToken("guest-token", auth.NewUser("3", "guest", []string{"guest"}))

	// 创建认证中间件
	authConfig := auth.Config{
		Name: "example-auth",
		TokenExtractor: auth.NewMultiTokenExtractor(
			auth.NewHeaderTokenExtractor("Authorization", "Bearer "),
			auth.NewQueryTokenExtractor("token"),
		),
		Authenticator: authenticator,
		SkipPaths:     []string{"/health", "/status", "/public"},
		EnableMetrics: true,
		OnAuthSuccess: func(ctx context.Context, user auth.User) {
			logger.Info("认证成功", "user_id", user.ID(), "username", user.Name())
		},
		OnAuthFailure: func(ctx context.Context, err error) {
			logger.Warn("认证失败", "error", err.Error())
		},
	}
	authMiddleware := auth.NewAuthMiddleware(authConfig)

	// 创建限流中间件
	rateLimitConfig := ratelimit.Config{
		Name:  "example-ratelimit",
		Rate:  10.0, // 每秒10个请求
		Burst: 20,   // 突发容量20
		KeyExtractor: ratelimit.NewChainKeyExtractor(
			ratelimit.NewUserKeyExtractor("user_id"), // 优先按用户限流
			ratelimit.NewIPKeyExtractor(),            // 其次按IP限流
		),
		EnableMetrics: true,
		OnRateLimit: func(ctx context.Context, key string, rate float64) {
			logger.Warn("请求被限流", "key", key, "rate", rate)
		},
	}
	rateLimitMiddleware := ratelimit.NewRateLimitMiddleware(rateLimitConfig)

	// 创建超时控制器
	timeoutController := timeout.NewTimeoutController(timeout.Config{
		Name:          "example-timeout",
		Timeout:       5 * time.Second,
		SlowThreshold: 2 * time.Second,
		EnableMetrics: true,
	})

	// 创建熔断器
	circuitBreaker := circuitbreaker.NewCircuitBreaker(circuitbreaker.Config{
		Name:        "example-circuit-breaker",
		MaxRequests: 3,
		Interval:    30 * time.Second,
		Timeout:     10 * time.Second,
		ReadyToTrip: func(counts circuitbreaker.Counts) bool {
			return counts.Requests >= 5 &&
				float64(counts.TotalFailures)/float64(counts.Requests) > 0.5
		},
	})

	// 创建 HTTP 路由
	mux := http.NewServeMux()

	// 公共端点（不需要认证）
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	})

	mux.HandleFunc("/public", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Public endpoint - no auth required"))
	})

	// 状态监控端点
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		authStats := authMiddleware.Stats()
		rlStats := rateLimitMiddleware.Stats()
		tcStats := timeoutController.Stats()
		cbStats := circuitBreaker.Counts()

		response := map[string]interface{}{
			"auth": map[string]interface{}{
				"name":             authMiddleware.Name(),
				"total_requests":   authStats.TotalRequests,
				"auth_requests":    authStats.AuthRequests,
				"success_requests": authStats.SuccessRequests,
				"failure_requests": authStats.FailureRequests,
				"skipped_requests": authStats.SkippedRequests,
				"success_rate":     fmt.Sprintf("%.2f%%", authMiddleware.SuccessRate()*100),
			},
			"rate_limit": map[string]interface{}{
				"name":             rateLimitMiddleware.Name(),
				"rate":             rateLimitMiddleware.LimitRate(),
				"burst":            rateLimitMiddleware.Burst(),
				"total_requests":   rlStats.TotalRequests,
				"allowed_requests": rlStats.AllowedRequests,
				"limited_requests": rlStats.LimitedRequests,
				"active_limiters":  rlStats.ActiveLimiters,
			},
			"timeout": map[string]interface{}{
				"name":             timeoutController.Name(),
				"timeout":          timeoutController.Timeout().String(),
				"total_requests":   tcStats.TotalRequests,
				"timeout_requests": tcStats.TimeoutRequests,
				"slow_requests":    tcStats.SlowRequests,
			},
			"circuit_breaker": map[string]interface{}{
				"name":     circuitBreaker.Name(),
				"state":    circuitBreaker.State().String(),
				"requests": cbStats.Requests,
				"failures": cbStats.TotalFailures,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	// 需要认证的端点
	mux.HandleFunc("/protected", func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.GetUserFromContext(r.Context())
		if !ok {
			http.Error(w, "No user found", http.StatusInternalServerError)
			return
		}

		response := map[string]interface{}{
			"message":  "Protected endpoint accessed successfully",
			"user_id":  user.ID(),
			"username": user.Name(),
			"roles":    user.Roles(),
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	// 需要管理员权限的端点
	mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.GetUserFromContext(r.Context())
		if !ok {
			http.Error(w, "No user found", http.StatusInternalServerError)
			return
		}

		if !user.HasRole("admin") {
			http.Error(w, "Admin role required", http.StatusForbidden)
			return
		}

		w.Write([]byte("Admin endpoint - admin access granted"))
	})

	// 模拟慢端点
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
		w.Write([]byte("Slow endpoint completed"))
	})

	// 自定义日志中间件
	loggingMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// 获取用户信息（如果有）
			userInfo := "anonymous"
			if user, ok := auth.GetUserFromContext(r.Context()); ok {
				userInfo = fmt.Sprintf("%s(%s)", user.Name(), user.ID())
			}

			next.ServeHTTP(w, r)

			duration := time.Since(start)
			logger.Info("HTTP请求完成",
				"method", r.Method,
				"path", r.URL.Path,
				"user", userInfo,
				"duration", duration.String(),
				"remote_addr", r.RemoteAddr)
		})
	}

	// 创建应用 - 展示完整的中间件组合
	app := beauty.New(
		// Web 服务器 - 组合使用所有中间件
		beauty.WithService(webserver.New(":8080", mux,
			webserver.WithServiceName("web-server"),
			// 中间件执行顺序（从外到内）：
			webserver.WithMiddleware(loggingMiddleware),  // 1. 日志中间件（最外层）
			webserver.WithAuth(authMiddleware),           // 2. 认证中间件
			webserver.WithRateLimit(rateLimitMiddleware), // 3. 限流中间件
			webserver.WithTimeout(timeoutController),     // 4. 超时控制中间件
			webserver.WithCircuitBreaker(circuitBreaker), // 5. 熔断器中间件（最内层）
		)),

		// gRPC 服务器 - 组合使用所有拦截器
		beauty.WithService(grpcserver.New(":9090", func(s *grpc.Server) {
			// 这里可以注册 gRPC 服务
		},
			grpcserver.WithServiceName("grpc-server"),
			// 拦截器执行顺序：
			grpcserver.WithAuth(authMiddleware),           // 1. 认证拦截器
			grpcserver.WithRateLimit(rateLimitMiddleware), // 2. 限流拦截器
			grpcserver.WithTimeout(timeoutController),     // 3. 超时控制拦截器
			grpcserver.WithCircuitBreaker(circuitBreaker), // 4. 熔断器拦截器
		)),
	)

	// 启动测试请求
	go func() {
		time.Sleep(2 * time.Second) // 等待服务启动

		logger.Info("开始发送测试请求...")

		// 测试不同的认证场景
		testCases := []struct {
			name     string
			endpoint string
			token    string
		}{
			{"无认证访问公共端点", "/public", ""},
			{"管理员访问保护端点", "/protected", "admin-token"},
			{"用户访问保护端点", "/protected", "user-token"},
			{"访客访问保护端点", "/protected", "guest-token"},
			{"无效token访问保护端点", "/protected", "invalid-token"},
			{"管理员访问管理端点", "/admin", "admin-token"},
			{"用户访问管理端点", "/admin", "user-token"},
			{"访问慢端点", "/slow", "admin-token"},
		}

		for i, tc := range testCases {
			go func(i int, tc struct {
				name     string
				endpoint string
				token    string
			}) {
				client := &http.Client{Timeout: 10 * time.Second}
				req, _ := http.NewRequest("GET", "http://localhost:8080"+tc.endpoint, nil)

				if tc.token != "" {
					req.Header.Set("Authorization", "Bearer "+tc.token)
				}

				resp, err := client.Do(req)
				if err != nil {
					logger.Error("HTTP请求失败",
						"test", tc.name,
						"error", err,
						"request", i)
					return
				}
				defer resp.Body.Close()

				logger.Info("HTTP请求完成",
					"test", tc.name,
					"status", resp.StatusCode,
					"request", i)
			}(i, tc)

			time.Sleep(500 * time.Millisecond)
		}

		// 测试限流
		logger.Info("开始测试限流...")
		for i := 0; i < 30; i++ {
			go func(i int) {
				client := &http.Client{Timeout: 5 * time.Second}
				req, _ := http.NewRequest("GET", "http://localhost:8080/protected", nil)
				req.Header.Set("Authorization", "Bearer admin-token")

				resp, err := client.Do(req)
				if err != nil {
					logger.Error("限流测试请求失败", "error", err, "request", i)
					return
				}
				defer resp.Body.Close()

				if resp.StatusCode == http.StatusTooManyRequests {
					logger.Warn("请求被限流", "request", i)
				} else {
					logger.Info("限流测试请求成功", "request", i)
				}
			}(i)

			time.Sleep(50 * time.Millisecond) // 快速发送请求测试限流
		}
	}()

	// 启动状态监控
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			authStats := authMiddleware.Stats()
			rlStats := rateLimitMiddleware.Stats()
			tcStats := timeoutController.Stats()
			cbStats := circuitBreaker.Counts()

			logger.Info("中间件状态汇总",
				// 认证统计
				"auth_total", authStats.TotalRequests,
				"auth_success", authStats.SuccessRequests,
				"auth_failure", authStats.FailureRequests,
				"auth_success_rate", fmt.Sprintf("%.1f%%", authMiddleware.SuccessRate()*100),

				// 限流统计
				"rl_total", rlStats.TotalRequests,
				"rl_allowed", rlStats.AllowedRequests,
				"rl_limited", rlStats.LimitedRequests,
				"rl_active_limiters", rlStats.ActiveLimiters,

				// 超时统计
				"tc_total", tcStats.TotalRequests,
				"tc_timeout", tcStats.TimeoutRequests,
				"tc_slow", tcStats.SlowRequests,

				// 熔断器统计
				"cb_state", circuitBreaker.State().String(),
				"cb_requests", cbStats.Requests,
				"cb_failures", cbStats.TotalFailures)
		}
	}()

	logger.Info("启动服务...")
	logger.Info("Web服务器: http://localhost:8080")
	logger.Info("gRPC服务器: localhost:9090")
	logger.Info("")
	logger.Info("测试端点:")
	logger.Info("  - 健康检查: http://localhost:8080/health")
	logger.Info("  - 公共端点: http://localhost:8080/public")
	logger.Info("  - 状态监控: http://localhost:8080/status")
	logger.Info("  - 保护端点: http://localhost:8080/protected (需要认证)")
	logger.Info("  - 管理端点: http://localhost:8080/admin (需要admin角色)")
	logger.Info("  - 慢端点: http://localhost:8080/slow (测试超时)")
	logger.Info("")
	logger.Info("认证令牌:")
	logger.Info("  - admin-token (admin角色)")
	logger.Info("  - user-token (user角色)")
	logger.Info("  - guest-token (guest角色)")
	logger.Info("")
	logger.Info("使用示例:")
	logger.Info("  curl http://localhost:8080/public")
	logger.Info("  curl -H \"Authorization: Bearer admin-token\" http://localhost:8080/protected")
	logger.Info("  curl -H \"Authorization: Bearer admin-token\" http://localhost:8080/admin")

	if err := app.Start(context.Background()); err != nil {
		logger.Error("应用启动失败", "error", err)
	}
}
