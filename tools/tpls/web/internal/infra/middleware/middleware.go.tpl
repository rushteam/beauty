package middleware

import (
	"net/http"

	"github.com/rushteam/beauty/pkg/middleware/auth"
	"github.com/rushteam/beauty/pkg/middleware/circuitbreaker"
	"github.com/rushteam/beauty/pkg/middleware/ratelimit"
	"github.com/rushteam/beauty/pkg/middleware/timeout"
	"github.com/rushteam/beauty/pkg/service/webserver"
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

// GetWebServerOptions 获取Web服务器选项
//
// 各中间件均实现为 func(http.Handler) http.Handler，统一通过
// webserver.WithMiddleware 注入（注入顺序即外层到内层的执行顺序）。
func (m *Middleware) GetWebServerOptions() []webserver.Option {
	var mws []func(http.Handler) http.Handler

	// 认证中间件
	if m.cfg.IsAuthEnabled() {
		authMiddleware := auth.NewAuthMiddleware(auth.Config{
			TokenExtractor: auth.NewHeaderTokenExtractor("Authorization", "Bearer "),
			Authenticator:  m.createAuthenticator(),
			SkipPaths:      m.cfg.Middleware.Auth.SkipPaths,
		})
		mws = append(mws, auth.HTTPMiddleware(authMiddleware))
	}

	// 限流中间件
	if m.cfg.IsRateLimitEnabled() {
		rateLimitMiddleware := ratelimit.NewRateLimitMiddleware(ratelimit.Config{
			Rate:         m.cfg.Middleware.RateLimit.Rate,
			Burst:        m.cfg.Middleware.RateLimit.Burst,
			KeyExtractor: ratelimit.NewIPKeyExtractor(),
		})
		mws = append(mws, ratelimit.HTTPMiddleware(rateLimitMiddleware))
	}

	// 超时控制中间件
	if m.cfg.IsTimeoutEnabled() {
		timeoutController := timeout.NewTimeoutController(timeout.Config{
			Timeout:       m.cfg.Middleware.Timeout.Timeout,
			SlowThreshold: m.cfg.Middleware.Timeout.SlowThreshold,
		})
		mws = append(mws, timeout.HTTPMiddleware(timeoutController))
	}

	// 熔断器中间件
	if m.cfg.IsCircuitBreakerEnabled() {
		circuitBreaker := circuitbreaker.NewCircuitBreaker(circuitbreaker.Config{
			MaxRequests: uint32(m.cfg.Middleware.CircuitBreaker.MaxRequests),
			Interval:    m.cfg.Middleware.CircuitBreaker.Interval,
			Timeout:     m.cfg.Middleware.CircuitBreaker.Timeout,
		})
		mws = append(mws, circuitbreaker.HTTPMiddleware(circuitBreaker))
	}

	if len(mws) == 0 {
		return nil
	}
	return []webserver.Option{webserver.WithMiddleware(mws...)}
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
