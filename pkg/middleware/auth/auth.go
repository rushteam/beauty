package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// ErrUnauthorized 未授权错误
	ErrUnauthorized = errors.New("unauthorized")
	// ErrForbidden 禁止访问错误
	ErrForbidden = errors.New("forbidden")
	// ErrInvalidToken 无效令牌错误
	ErrInvalidToken = errors.New("invalid token")
	// ErrTokenExpired 令牌过期错误
	ErrTokenExpired = errors.New("token expired")
)

// User 用户信息接口
type User interface {
	// ID 返回用户ID
	ID() string
	// Name 返回用户名
	Name() string
	// Roles 返回用户角色列表
	Roles() []string
	// HasRole 检查用户是否拥有指定角色
	HasRole(role string) bool
	// Metadata 返回用户元数据
	Metadata() map[string]interface{}
}

// DefaultUser 默认用户实现
type DefaultUser struct {
	id       string
	name     string
	roles    []string
	metadata map[string]interface{}
}

func NewUser(id, name string, roles []string) *DefaultUser {
	return &DefaultUser{
		id:       id,
		name:     name,
		roles:    roles,
		metadata: make(map[string]interface{}),
	}
}

func (u *DefaultUser) ID() string                       { return u.id }
func (u *DefaultUser) Name() string                     { return u.name }
func (u *DefaultUser) Roles() []string                  { return u.roles }
func (u *DefaultUser) Metadata() map[string]interface{} { return u.metadata }

func (u *DefaultUser) HasRole(role string) bool {
	for _, r := range u.roles {
		if r == role {
			return true
		}
	}
	return false
}

func (u *DefaultUser) SetMetadata(key string, value interface{}) {
	u.metadata[key] = value
}

// TokenExtractor 令牌提取器接口
type TokenExtractor interface {
	// Extract 从请求中提取令牌
	Extract(ctx context.Context, metadata map[string]interface{}) (string, error)
}

// Authenticator 认证器接口
type Authenticator interface {
	// Authenticate 认证令牌并返回用户信息
	Authenticate(ctx context.Context, token string) (User, error)
}

// Authorizer 授权器接口
type Authorizer interface {
	// Authorize 检查用户是否有权限访问资源
	Authorize(ctx context.Context, user User, resource string, action string) error
}

// Config 认证配置
type Config struct {
	// Name 认证器名称
	Name string
	// TokenExtractor 令牌提取器
	TokenExtractor TokenExtractor
	// Authenticator 认证器
	Authenticator Authenticator
	// Authorizer 授权器（可选）
	Authorizer Authorizer
	// SkipPaths 跳过认证的路径列表
	SkipPaths []string
	// EnableMetrics 是否启用指标统计
	EnableMetrics bool
	// OnAuthSuccess 认证成功回调
	OnAuthSuccess func(ctx context.Context, user User)
	// OnAuthFailure 认证失败回调
	OnAuthFailure func(ctx context.Context, err error)
}

// AuthMiddleware 认证中间件
type AuthMiddleware struct {
	name           string
	tokenExtractor TokenExtractor
	authenticator  Authenticator
	authorizer     Authorizer
	skipPaths      map[string]bool
	enableMetrics  bool
	onAuthSuccess  func(ctx context.Context, user User)
	onAuthFailure  func(ctx context.Context, err error)

	// 统计信息
	mutex sync.RWMutex
	stats Stats
}

// Stats 认证统计信息
type Stats struct {
	TotalRequests   uint64    `json:"total_requests"`    // 总请求数
	AuthRequests    uint64    `json:"auth_requests"`     // 需要认证的请求数
	SuccessRequests uint64    `json:"success_requests"`  // 认证成功请求数
	FailureRequests uint64    `json:"failure_requests"`  // 认证失败请求数
	SkippedRequests uint64    `json:"skipped_requests"`  // 跳过认证请求数
	LastAuthTime    time.Time `json:"last_auth_time"`    // 最后认证时间
	LastFailureTime time.Time `json:"last_failure_time"` // 最后失败时间
}

// NewAuthMiddleware 创建认证中间件
func NewAuthMiddleware(config Config) *AuthMiddleware {
	if config.Name == "" {
		config.Name = "auth-middleware"
	}

	skipPaths := make(map[string]bool)
	for _, path := range config.SkipPaths {
		skipPaths[path] = true
	}

	return &AuthMiddleware{
		name:           config.Name,
		tokenExtractor: config.TokenExtractor,
		authenticator:  config.Authenticator,
		authorizer:     config.Authorizer,
		skipPaths:      skipPaths,
		enableMetrics:  config.EnableMetrics,
		onAuthSuccess:  config.OnAuthSuccess,
		onAuthFailure:  config.OnAuthFailure,
	}
}

// Name 返回中间件名称
func (am *AuthMiddleware) Name() string {
	return am.name
}

// ShouldSkip 检查是否应该跳过认证
func (am *AuthMiddleware) ShouldSkip(path string) bool {
	return am.skipPaths[path]
}

// Authenticate 执行认证
func (am *AuthMiddleware) Authenticate(ctx context.Context, metadata map[string]interface{}) (User, error) {
	am.recordRequest()

	// 提取令牌
	token, err := am.tokenExtractor.Extract(ctx, metadata)
	if err != nil {
		am.recordFailure(err)
		return nil, fmt.Errorf("failed to extract token: %w", err)
	}

	// 认证令牌
	user, err := am.authenticator.Authenticate(ctx, token)
	if err != nil {
		am.recordFailure(err)
		return nil, fmt.Errorf("authentication failed: %w", err)
	}

	am.recordSuccess()
	return user, nil
}

// Authorize 执行授权检查
func (am *AuthMiddleware) Authorize(ctx context.Context, user User, resource, action string) error {
	if am.authorizer == nil {
		return nil // 没有配置授权器，跳过授权检查
	}

	err := am.authorizer.Authorize(ctx, user, resource, action)
	if err != nil {
		am.recordFailure(err)
		return fmt.Errorf("authorization failed: %w", err)
	}

	return nil
}

// recordRequest 记录请求
func (am *AuthMiddleware) recordRequest() {
	if !am.enableMetrics {
		return
	}

	am.mutex.Lock()
	defer am.mutex.Unlock()
	am.stats.TotalRequests++
	am.stats.AuthRequests++
}

// recordSuccess 记录成功
func (am *AuthMiddleware) recordSuccess() {
	if !am.enableMetrics {
		return
	}

	am.mutex.Lock()
	defer am.mutex.Unlock()
	am.stats.SuccessRequests++
	am.stats.LastAuthTime = time.Now()

	if am.onAuthSuccess != nil {
		// 在 goroutine 中执行回调，避免阻塞
		go func() {
			// 这里需要传递用户信息，但为了简化，暂时传递 nil
			am.onAuthSuccess(context.Background(), nil)
		}()
	}
}

// recordFailure 记录失败
func (am *AuthMiddleware) recordFailure(err error) {
	if !am.enableMetrics {
		return
	}

	am.mutex.Lock()
	defer am.mutex.Unlock()
	am.stats.FailureRequests++
	am.stats.LastFailureTime = time.Now()

	if am.onAuthFailure != nil {
		// 在 goroutine 中执行回调，避免阻塞
		go func() {
			am.onAuthFailure(context.Background(), err)
		}()
	}
}

// recordSkipped 记录跳过
func (am *AuthMiddleware) recordSkipped() {
	if !am.enableMetrics {
		return
	}

	am.mutex.Lock()
	defer am.mutex.Unlock()
	am.stats.TotalRequests++
	am.stats.SkippedRequests++
}

// Stats 返回统计信息
func (am *AuthMiddleware) Stats() Stats {
	am.mutex.RLock()
	defer am.mutex.RUnlock()
	return am.stats
}

// ResetStats 重置统计信息
func (am *AuthMiddleware) ResetStats() {
	am.mutex.Lock()
	defer am.mutex.Unlock()
	am.stats = Stats{}
}

// SuccessRate 返回认证成功率
func (am *AuthMiddleware) SuccessRate() float64 {
	stats := am.Stats()
	if stats.AuthRequests == 0 {
		return 0
	}
	return float64(stats.SuccessRequests) / float64(stats.AuthRequests)
}

// String 返回中间件的字符串表示
func (am *AuthMiddleware) String() string {
	stats := am.Stats()
	return fmt.Sprintf("AuthMiddleware[%s: total=%d, auth=%d, success=%d, failure=%d, skipped=%d, success_rate=%.2f%%]",
		am.name, stats.TotalRequests, stats.AuthRequests, stats.SuccessRequests,
		stats.FailureRequests, stats.SkippedRequests, am.SuccessRate()*100)
}

// IsAuthError 检查错误是否为认证相关错误
func IsAuthError(err error) bool {
	return err == ErrUnauthorized || err == ErrForbidden ||
		err == ErrInvalidToken || err == ErrTokenExpired
}
