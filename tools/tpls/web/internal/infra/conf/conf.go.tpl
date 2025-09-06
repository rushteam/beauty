package conf

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Server 服务器配置
type Server struct {
	App  string `mapstructure:"app" yaml:"app"`
	HTTP HTTP   `mapstructure:"http" yaml:"http"`
	Log  Log    `mapstructure:"log" yaml:"log"`
}

// HTTP HTTP服务器配置
type HTTP struct {
	Addr         string        `mapstructure:"addr" yaml:"addr"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout" yaml:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout" yaml:"write_timeout"`
	IdleTimeout  time.Duration `mapstructure:"idle_timeout" yaml:"idle_timeout"`
}

// GRPC gRPC服务器配置
type GRPC struct {
	Addr    string        `mapstructure:"addr" yaml:"addr"`
	Timeout time.Duration `mapstructure:"timeout" yaml:"timeout"`
}

// Log 日志配置
type Log struct {
	Level  string `mapstructure:"level" yaml:"level"`
	Format string `mapstructure:"format" yaml:"format"`
	Output string `mapstructure:"output" yaml:"output"`
}

// Database 数据库配置
type Database struct {
	Driver string `mapstructure:"driver" yaml:"driver"`
	DSN    string `mapstructure:"dsn" yaml:"dsn"`
}

// Redis Redis配置
type Redis struct {
	Addr     string `mapstructure:"addr" yaml:"addr"`
	Password string `mapstructure:"password" yaml:"password"`
	DB       int    `mapstructure:"db" yaml:"db"`
}

// Registry 服务注册配置
type Registry struct {
	Type      string            `mapstructure:"type" yaml:"type"`
	Endpoints []string          `mapstructure:"endpoints" yaml:"endpoints"`
	Config    map[string]string `mapstructure:"config" yaml:"config"`
}

// Middleware 中间件配置
type Middleware struct {
	Auth           Auth           `mapstructure:"auth" yaml:"auth"`
	RateLimit      RateLimit      `mapstructure:"rate_limit" yaml:"rate_limit"`
	Timeout        Timeout        `mapstructure:"timeout" yaml:"timeout"`
	CircuitBreaker CircuitBreaker `mapstructure:"circuit_breaker" yaml:"circuit_breaker"`
}

// Auth 认证配置
type Auth struct {
	Enabled   bool     `mapstructure:"enabled" yaml:"enabled"`
	Type      string   `mapstructure:"type" yaml:"type"`
	Secret    string   `mapstructure:"secret" yaml:"secret"`
	SkipPaths []string `mapstructure:"skip_paths" yaml:"skip_paths"`
}

// RateLimit 限流配置
type RateLimit struct {
	Enabled bool    `mapstructure:"enabled" yaml:"enabled"`
	Rate    float64 `mapstructure:"rate" yaml:"rate"`
	Burst   int     `mapstructure:"burst" yaml:"burst"`
}

// Timeout 超时配置
type Timeout struct {
	Enabled       bool          `mapstructure:"enabled" yaml:"enabled"`
	Timeout       time.Duration `mapstructure:"timeout" yaml:"timeout"`
	SlowThreshold time.Duration `mapstructure:"slow_threshold" yaml:"slow_threshold"`
}

// CircuitBreaker 熔断器配置
type CircuitBreaker struct {
	Enabled     bool          `mapstructure:"enabled" yaml:"enabled"`
	MaxRequests int           `mapstructure:"max_requests" yaml:"max_requests"`
	Interval    time.Duration `mapstructure:"interval" yaml:"interval"`
	Timeout     time.Duration `mapstructure:"timeout" yaml:"timeout"`
}

// Load 从YAML文件加载配置
func Load(configPath string, cfg interface{}) error {
	// 检查配置文件是否存在
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return fmt.Errorf("配置文件不存在: %s", configPath)
	}

	// 读取配置文件
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("读取配置文件失败: %w", err)
	}

	// 解析YAML
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("解析配置文件失败: %w", err)
	}

	return nil
}
