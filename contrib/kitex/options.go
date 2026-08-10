package kitex

import (
	"fmt"
	"maps"

	kserver "github.com/cloudwego/kitex/server"
	"github.com/rushteam/beauty/pkg/service/discover"
)

// Option 是 Server 的配置选项。
type Option func(*Server)

// WithServiceName 设置服务名（用于注册中心和日志）。
func WithServiceName(name string) Option {
	return func(s *Server) {
		s.name = name
	}
}

// WithMetadata 设置自定义元数据，会与默认元数据合并。
func WithMetadata(md map[string]string) Option {
	return func(s *Server) {
		maps.Copy(s.metadata, md)
	}
}

// WithKitexServerOptions 透传 Kitex 原生 server.Option。
// 例如 server.WithMetaHandler、server.WithExitWaitTime 等。
func WithKitexServerOptions(opts ...kserver.Option) Option {
	return func(s *Server) {
		s.kitexOpts = append(s.kitexOpts, opts...)
	}
}

// WithAutoServiceDiscovery 启用自动服务发现，为每个 Thrift 服务单独注册。
func WithAutoServiceDiscovery(registries []discover.Registry, sdOpts ...ServiceDiscoveryOption) Option {
	return func(s *Server) {
		s.autoDiscover = true
		s.serviceDiscovery = NewServiceDiscovery(registries, sdOpts...)
	}
}

// WithRegionInfo 设置地域信息，兼容 Polaris。
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

// WithVersion 设置服务版本。
func WithVersion(version string) Option {
	return func(s *Server) {
		s.metadata["version"] = version
	}
}
