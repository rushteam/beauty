package middleware

import (
	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/middleware/auth"
	"github.com/rushteam/beauty/pkg/middleware/circuitbreaker"
	"github.com/rushteam/beauty/pkg/middleware/ratelimit"
	"github.com/rushteam/beauty/pkg/middleware/timeout"
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

// GetOptions 获取Beauty选项
func (m *Middleware) GetOptions() []beauty.Option {
	var options []beauty.Option

	// 认证中间件
	if m.cfg.IsAuthEnabled() {
		authMiddleware := auth.NewAuthMiddleware(auth.Config{
			TokenExtractor: auth.NewHeaderTokenExtractor("Authorization", "Bearer "),
			Authenticator:  m.createAuthenticator(),
			SkipPaths:     m.cfg.Middleware.Auth.SkipPaths,
		})
		options = append(options, beauty.WithWebMiddleware(auth.HTTPMiddleware(authMiddleware)))
	}

	// 限流中间件
	if m.cfg.IsRateLimitEnabled() {
		rateLimitMiddleware := ratelimit.NewRateLimitMiddleware(ratelimit.Config{
			Rate:         m.cfg.Middleware.RateLimit.Rate,
			Burst:        m.cfg.Middleware.RateLimit.Burst,
			KeyExtractor: ratelimit.NewIPKeyExtractor(),
		})
		options = append(options, beauty.WithWebMiddleware(ratelimit.HTTPMiddleware(rateLimitMiddleware)))
	}

	// 超时控制中间件
	if m.cfg.IsTimeoutEnabled() {
		timeoutController := timeout.NewTimeoutController(timeout.Config{
			Timeout:       m.cfg.Middleware.Timeout.Timeout,
			SlowThreshold: m.cfg.Middleware.Timeout.SlowThreshold,
		})
		options = append(options, beauty.WithWebMiddleware(timeout.HTTPMiddleware(timeoutController)))
	}

	// 熔断器中间件
	if m.cfg.IsCircuitBreakerEnabled() {
		circuitBreaker := circuitbreaker.NewCircuitBreaker(circuitbreaker.Config{
			MaxRequests: m.cfg.Middleware.CircuitBreaker.MaxRequests,
			Interval:    m.cfg.Middleware.CircuitBreaker.Interval,
			Timeout:     m.cfg.Middleware.CircuitBreaker.Timeout,
		})
		options = append(options, beauty.WithWebMiddleware(circuitbreaker.HTTPMiddleware(circuitBreaker)))
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
