package middleware

import (
	"github.com/rushteam/beauty/pkg/middleware/auth"
	"github.com/rushteam/beauty/pkg/middleware/circuitbreaker"
	"github.com/rushteam/beauty/pkg/middleware/ratelimit"
	"github.com/rushteam/beauty/pkg/middleware/timeout"
	"github.com/rushteam/beauty/pkg/service/grpcserver"
	"{{.ImportPath}}internal/config"
)

// Middleware 中间件管理器
type Middleware struct {
	cfg *config.Config
}

// New 创建中间件管理器
func New(cfg *config.Config) *Middleware {
	return &Middleware{cfg: cfg}
}

// GetGrpcServerOptions 获取gRPC服务器选项
func (m *Middleware) GetGrpcServerOptions() []grpcserver.Options {
	var options []grpcserver.Options

	// 认证拦截器
	if m.cfg.IsAuthEnabled() {
		authMiddleware := auth.NewAuthMiddleware(auth.Config{
			TokenExtractor: auth.NewHeaderTokenExtractor("Authorization", "Bearer "),
			Authenticator:  m.createAuthenticator(),
			SkipPaths:     m.cfg.Middleware.Auth.SkipPaths,
		})
		options = append(options, grpcserver.WithAuth(authMiddleware))
	}

	// 限流拦截器
	if m.cfg.IsRateLimitEnabled() {
		rateLimitMiddleware := ratelimit.NewRateLimitMiddleware(ratelimit.Config{
			Rate:         m.cfg.Middleware.RateLimit.Rate,
			Burst:        m.cfg.Middleware.RateLimit.Burst,
			KeyExtractor: ratelimit.NewIPKeyExtractor(),
		})
		options = append(options, grpcserver.WithRateLimit(rateLimitMiddleware))
	}

	// 超时控制拦截器
	if m.cfg.IsTimeoutEnabled() {
		timeoutController := timeout.NewTimeoutController(timeout.Config{
			Timeout:       m.cfg.Middleware.Timeout.Timeout,
			SlowThreshold: m.cfg.Middleware.Timeout.SlowThreshold,
		})
		options = append(options, grpcserver.WithTimeout(timeoutController))
	}

	// 熔断器拦截器
	if m.cfg.IsCircuitBreakerEnabled() {
		circuitBreaker := circuitbreaker.NewCircuitBreaker(circuitbreaker.Config{
			MaxRequests: uint32(m.cfg.Middleware.CircuitBreaker.MaxRequests),
			Interval:    m.cfg.Middleware.CircuitBreaker.Interval,
			Timeout:     m.cfg.Middleware.CircuitBreaker.Timeout,
		})
		options = append(options, grpcserver.WithCircuitBreaker(circuitBreaker))
	}

	return options
}

// createAuthenticator 创建认证器
func (m *Middleware) createAuthenticator() auth.Authenticator {
	switch m.cfg.Middleware.Auth.Type {
	case "jwt":
		return auth.NewSimpleJWTAuthenticator(m.cfg.Middleware.Auth.Secret)
	case "static":
		authenticator := auth.NewStaticTokenAuthenticator()
		// 添加测试令牌
		authenticator.AddToken("test-token", auth.NewUser("test-user", "Test User", []string{"user"}))
		return authenticator
	default:
		authenticator := auth.NewStaticTokenAuthenticator()
		// 添加测试令牌
		authenticator.AddToken("test-token", auth.NewUser("test-user", "Test User", []string{"user"}))
		return authenticator
	}
}
