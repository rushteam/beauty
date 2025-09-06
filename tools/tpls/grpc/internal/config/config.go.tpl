package config

import (
	"{{.ImportPath}}internal/infra/conf"
)

// Config 应用配置
type Config struct {
	conf.Server `mapstructure:",squash"`
	GRPC        conf.GRPC       `mapstructure:"grpc" yaml:"grpc"`
	Registry    conf.Registry   `mapstructure:"registry" yaml:"registry"`
	Middleware  conf.Middleware `mapstructure:"middleware" yaml:"middleware"`
}

// GetAppName 获取应用名称
func (c *Config) GetAppName() string {
	return c.App
}

// GetGRPCAddr 获取gRPC地址
func (c *Config) GetGRPCAddr() string {
	return c.GRPC.Addr
}

// IsAuthEnabled 是否启用认证
func (c *Config) IsAuthEnabled() bool {
	return c.Middleware.Auth.Enabled
}

// IsRateLimitEnabled 是否启用限流
func (c *Config) IsRateLimitEnabled() bool {
	return c.Middleware.RateLimit.Enabled
}

// IsTimeoutEnabled 是否启用超时控制
func (c *Config) IsTimeoutEnabled() bool {
	return c.Middleware.Timeout.Enabled
}

// IsCircuitBreakerEnabled 是否启用熔断器
func (c *Config) IsCircuitBreakerEnabled() bool {
	return c.Middleware.CircuitBreaker.Enabled
}
