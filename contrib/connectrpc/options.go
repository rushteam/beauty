package connectrpc

import (
	"crypto/tls"
	"fmt"
	"maps"
	"net/http"
	"time"

	"github.com/rushteam/beauty/pkg/service/discover"
)

// Option 是 Server 的功能选项。
type Option func(*Server)

// WithServiceName 设置服务名称，用于日志和 OTel span name。默认 "connect-server"。
func WithServiceName(name string) Option {
	return func(s *Server) {
		s.name = name
	}
}

// WithShutdownTimeout 设置优雅关闭的最长等待时间，默认 30s。
func WithShutdownTimeout(d time.Duration) Option {
	return func(s *Server) {
		s.shutdownTimeout = d
	}
}

// WithReadTimeout 设置 HTTP 读超时。默认 0（不限）。
func WithReadTimeout(d time.Duration) Option {
	return func(s *Server) {
		s.readTimeout = d
	}
}

// WithWriteTimeout 设置 HTTP 写超时。默认 0（不限），适合流式响应。
func WithWriteTimeout(d time.Duration) Option {
	return func(s *Server) {
		s.writeTimeout = d
	}
}

// WithIdleTimeout 设置空闲连接超时。默认 0（不限）。
func WithIdleTimeout(d time.Duration) Option {
	return func(s *Server) {
		s.idleTimeout = d
	}
}

// WithVersion 将服务版本写入 metadata，注册到注册中心后可见，灰度发布时可用于流量路由。
func WithVersion(version string) Option {
	return func(s *Server) {
		s.metadata["version"] = version
	}
}

// WithMetadata 设置自定义元数据，会合并到已有 metadata。
func WithMetadata(md map[string]string) Option {
	return func(s *Server) {
		maps.Copy(s.metadata, md)
	}
}

// WithMiddleware 添加 HTTP 中间件，按添加顺序从外到内执行。
// Connect handler 本身就是 http.Handler，所以现有的 HTTP 中间件
// （accesslog、cors、ratelimit 等）都可以直接使用。
func WithMiddleware(middlewares ...func(http.Handler) http.Handler) Option {
	return func(s *Server) {
		s.middlewares = append(s.middlewares, middlewares...)
	}
}

// WithH2C 控制是否启用 HTTP/2 Cleartext（h2c）。默认启用。
// 启用后 Connect 服务可同时处理 HTTP/1.1 和 HTTP/2 请求（含 gRPC 协议）。
// 如果使用 TLS，则自动禁用 h2c（HTTP/2 通过 ALPN 协商）。
func WithH2C(enable bool) Option {
	return func(s *Server) {
		s.enableH2C = enable
	}
}

// WithHealthCheck 控制是否自动注册 gRPC 健康检查端点。默认启用。
// 启用后会自动注册 grpc.health.v1.Health 服务，将通过 Handle 注册的
// protobuf 服务全部标记为 SERVING，兼容 grpcurl、grpc-health-probe
// 和 Kubernetes gRPC 探针。
func WithHealthCheck(enable bool) Option {
	return func(s *Server) {
		s.enableHealth = enable
	}
}

// WithTLS 通过证书文件启用 HTTPS。certFile 和 keyFile 为 PEM 格式文件路径。
// 启用 TLS 后 h2c 自动禁用，HTTP/2 通过 ALPN 协商。
func WithTLS(certFile, keyFile string) Option {
	return func(s *Server) {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			panic(fmt.Sprintf("connectrpc: failed to load TLS key pair: %v", err))
		}
		if s.tlsConfig == nil {
			s.tlsConfig = &tls.Config{}
		}
		s.tlsConfig.Certificates = append(s.tlsConfig.Certificates, cert)
	}
}

// WithTLSConfig 通过自定义 tls.Config 启用 HTTPS，适合 mTLS 或自定义 CA 场景。
func WithTLSConfig(cfg *tls.Config) Option {
	return func(s *Server) {
		s.tlsConfig = cfg
	}
}

// WithAutoServiceDiscovery 启用自动服务发现，为每个 protobuf 服务单独注册到注册中心。
// 可通过 sdOpts 传入 WithInternalServices() 控制是否注册健康检查等内部服务。
func WithAutoServiceDiscovery(registries []discover.Registry, sdOpts ...ServiceDiscoveryOption) Option {
	return func(s *Server) {
		s.autoDiscover = true
		s.serviceDiscovery = NewServiceDiscovery(registries, sdOpts...)
	}
}

// WithRegionInfo 设置地域信息，兼容 Polaris 等注册中心。
func WithRegionInfo(region, zone, campus string) Option {
	return func(s *Server) {
		s.metadata["region"] = region
		s.metadata["zone"] = zone
		s.metadata["campus"] = campus
	}
}

// WithEnvironment 设置环境信息。
func WithEnvironment(env string) Option {
	return func(s *Server) {
		s.metadata["environment"] = env
	}
}

// WithWeight 设置服务权重。
func WithWeight(weight int) Option {
	return func(s *Server) {
		s.metadata["weight"] = fmt.Sprintf("%d", weight)
	}
}

// WithPriority 设置服务优先级。
func WithPriority(priority int) Option {
	return func(s *Server) {
		s.metadata["priority"] = fmt.Sprintf("%d", priority)
	}
}
