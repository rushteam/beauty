package config

import (
	"{{.ImportPath}}internal/infra/conf"
)

// Config 应用配置
type Config struct {
	conf.Server `mapstructure:",squash"`
	Database    conf.Database    `mapstructure:"database" yaml:"database"`
	Redis       conf.Redis       `mapstructure:"redis" yaml:"redis"`
	Registry    conf.Registry    `mapstructure:"registry" yaml:"registry"`
	Middleware  conf.Middleware  `mapstructure:"middleware" yaml:"middleware"`
}

// GetAppName 获取应用名称
func (c *Config) GetAppName() string {
	return c.App
}

// GetHTTPAddr 获取HTTP地址
func (c *Config) GetHTTPAddr() string {
	return c.HTTP.Addr
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
