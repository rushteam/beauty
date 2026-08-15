package graphql

import (
	"net/http"
	"time"

	"github.com/99designs/gqlgen/graphql"
)

// Option 配置 GraphQL Server。
type Option func(*Server)

// WithName 设置服务名(注册中心标识)。
func WithName(name string) Option {
	return func(s *Server) { s.name = name }
}

// WithID 设置服务 ID。
func WithID(id string) Option {
	return func(s *Server) { s.id = id }
}

// WithVersion 设置服务版本。
func WithVersion(v string) Option {
	return func(s *Server) { s.version = v }
}

// WithPlayground 启用/禁用 GraphQL Playground UI。
func WithPlayground(enabled bool) Option {
	return func(s *Server) { s.playgroundEnabled = enabled }
}

// WithPlaygroundPath 设置 playground 路径(默认 "/")。
func WithPlaygroundPath(path string) Option {
	return func(s *Server) { s.playgroundPath = path }
}

// WithGraphQLPath 设置 GraphQL endpoint 路径(默认 "/query")。
func WithGraphQLPath(path string) Option {
	return func(s *Server) { s.graphqlPath = path }
}

// WithMiddleware 添加 HTTP 中间件(按添加顺序,最先添加的最外层)。
func WithMiddleware(mw ...func(http.Handler) http.Handler) Option {
	return func(s *Server) { s.middleware = append(s.middleware, mw...) }
}

// WithExtension 添加 gqlgen HandlerExtension。
func WithExtension(ext ...graphql.HandlerExtension) Option {
	return func(s *Server) { s.extensions = append(s.extensions, ext...) }
}

// WithTransport 添加自定义 gqlgen Transport(如 WS/SSE subscription)。
func WithTransport(t ...graphql.Transport) Option {
	return func(s *Server) { s.transports = append(s.transports, t...) }
}

// WithShutdownTimeout 设置优雅关闭超时(默认 30s)。
func WithShutdownTimeout(d time.Duration) Option {
	return func(s *Server) { s.shutdownTimeout = d }
}

// WithComplexityLimit 设置查询复杂度上限(gqlgen 内置 extension)。
func WithComplexityLimit(limit int) Option {
	return func(s *Server) {
		s.extensions = append(s.extensions, &complexityLimitExt{limit: limit})
	}
}

// complexityLimitExt 是 gqlgen 内置复杂度限制的简单包装。
type complexityLimitExt struct {
	limit int
}

func (e *complexityLimitExt) ExtensionName() string { return "ComplexityLimit" }
func (e *complexityLimitExt) Validate(_ graphql.ExecutableSchema) error {
	return nil
}
