package grpcserver

import (
	"context"
	"fmt"
	"log"
	"net"

	middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"github.com/rushteam/beauty/pkg/middleware/auth"
	"github.com/rushteam/beauty/pkg/middleware/circuitbreaker"
	"github.com/rushteam/beauty/pkg/middleware/ratelimit"
	"github.com/rushteam/beauty/pkg/middleware/timeout"
	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/service/logger"
	"github.com/rushteam/beauty/pkg/utils/addr"
	"github.com/rushteam/beauty/pkg/utils/uuid"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
)

var _ discover.Service = (*Server)(nil)

func WithGrpcServerOptions(opts ...grpc.ServerOption) Options {
	return func(s *Server) {
		s.grpcOpts = append(s.grpcOpts, opts...)
	}
}

func WithGrpcServerUnaryInterceptor(interceptors ...grpc.UnaryServerInterceptor) Options {
	return WithGrpcServerOptions(grpc.UnaryInterceptor(
		middleware.ChainUnaryServer(interceptors...),
	))
}

func WithGrpcServerStreamInterceptor(interceptors ...grpc.StreamServerInterceptor) Options {
	return WithGrpcServerOptions(grpc.StreamInterceptor(
		middleware.ChainStreamServer(interceptors...),
	))
}

// WithCircuitBreaker 添加熔断器拦截器
func WithCircuitBreaker(cb *circuitbreaker.CircuitBreaker) Options {
	return func(s *Server) {
		if s.unaryInterceptors == nil {
			s.unaryInterceptors = make([]grpc.UnaryServerInterceptor, 0)
		}
		if s.streamInterceptors == nil {
			s.streamInterceptors = make([]grpc.StreamServerInterceptor, 0)
		}
		s.unaryInterceptors = append(s.unaryInterceptors, circuitbreaker.UnaryServerInterceptor(cb))
		s.streamInterceptors = append(s.streamInterceptors, circuitbreaker.StreamServerInterceptor(cb))
	}
}

// WithTimeout 添加超时控制拦截器
func WithTimeout(tc *timeout.TimeoutController) Options {
	return func(s *Server) {
		if s.unaryInterceptors == nil {
			s.unaryInterceptors = make([]grpc.UnaryServerInterceptor, 0)
		}
		if s.streamInterceptors == nil {
			s.streamInterceptors = make([]grpc.StreamServerInterceptor, 0)
		}
		s.unaryInterceptors = append(s.unaryInterceptors, timeout.UnaryServerInterceptor(tc))
		s.streamInterceptors = append(s.streamInterceptors, timeout.StreamServerInterceptor(tc))
	}
}

// WithAuth 添加认证拦截器
func WithAuth(am *auth.AuthMiddleware) Options {
	return func(s *Server) {
		if s.unaryInterceptors == nil {
			s.unaryInterceptors = make([]grpc.UnaryServerInterceptor, 0)
		}
		if s.streamInterceptors == nil {
			s.streamInterceptors = make([]grpc.StreamServerInterceptor, 0)
		}
		s.unaryInterceptors = append(s.unaryInterceptors, auth.UnaryServerInterceptor(am))
		s.streamInterceptors = append(s.streamInterceptors, auth.StreamServerInterceptor(am))
	}
}

// WithRateLimit 添加限流拦截器
func WithRateLimit(rl *ratelimit.RateLimitMiddleware) Options {
	return func(s *Server) {
		if s.unaryInterceptors == nil {
			s.unaryInterceptors = make([]grpc.UnaryServerInterceptor, 0)
		}
		if s.streamInterceptors == nil {
			s.streamInterceptors = make([]grpc.StreamServerInterceptor, 0)
		}
		s.unaryInterceptors = append(s.unaryInterceptors, ratelimit.UnaryServerInterceptor(rl))
		s.streamInterceptors = append(s.streamInterceptors, ratelimit.StreamServerInterceptor(rl))
	}
}

// WithRateLimitWait 添加等待型限流拦截器
func WithRateLimitWait(rl *ratelimit.RateLimitMiddleware) Options {
	return func(s *Server) {
		if s.unaryInterceptors == nil {
			s.unaryInterceptors = make([]grpc.UnaryServerInterceptor, 0)
		}
		if s.streamInterceptors == nil {
			s.streamInterceptors = make([]grpc.StreamServerInterceptor, 0)
		}
		s.unaryInterceptors = append(s.unaryInterceptors, ratelimit.UnaryServerWaitInterceptor(rl))
		s.streamInterceptors = append(s.streamInterceptors, ratelimit.StreamServerInterceptor(rl))
	}
}

func WithServiceName(name string) Options {
	return func(s *Server) {
		s.name = name
	}
}

func WithMetadata(md map[string]string) Options {
	return func(s *Server) {
		for k, v := range md {
			s.metadata[k] = v
		}
	}
}

// WithAutoServiceDiscovery 启用自动服务发现，为每个protobuf服务单独注册
func WithAutoServiceDiscovery(registries ...discover.Registry) Options {
	return func(s *Server) {
		s.autoDiscover = true
		s.serviceDiscovery = NewServiceDiscovery(s.Server, registries...)
	}
}

// WithRegionInfo 设置地域信息，兼容Polaris
func WithRegionInfo(region, zone, campus string) Options {
	return func(s *Server) {
		if s.metadata == nil {
			s.metadata = make(map[string]string)
		}
		s.metadata["region"] = region
		s.metadata["zone"] = zone
		s.metadata["campus"] = campus
	}
}

// WithEnvironment 设置环境信息
func WithEnvironment(env string) Options {
	return func(s *Server) {
		if s.metadata == nil {
			s.metadata = make(map[string]string)
		}
		s.metadata["environment"] = env
	}
}

// WithWeight 设置服务权重
func WithWeight(weight int) Options {
	return func(s *Server) {
		if s.metadata == nil {
			s.metadata = make(map[string]string)
		}
		s.metadata["weight"] = fmt.Sprintf("%d", weight)
	}
}

// WithPriority 设置服务优先级
func WithPriority(priority int) Options {
	return func(s *Server) {
		if s.metadata == nil {
			s.metadata = make(map[string]string)
		}
		s.metadata["priority"] = fmt.Sprintf("%d", priority)
	}
}

type Options func(*Server)

// New create a grpc service with the name
func New(addr string, handler func(*grpc.Server), opts ...Options) *Server {
	s := &Server{
		id:                 uuid.New(),
		name:               "grpc-server",
		metadata:           map[string]string{"kind": "grpc"},
		unaryInterceptors:  make([]grpc.UnaryServerInterceptor, 0),
		streamInterceptors: make([]grpc.StreamServerInterceptor, 0),
		addr:               addr,
		Server:             nil,
	}

	// 应用选项
	for _, o := range opts {
		o(s)
	}

	// 构建 gRPC 选项
	grpcOpts := s.grpcOpts

	// 添加拦截器链
	if len(s.unaryInterceptors) > 0 {
		grpcOpts = append(grpcOpts, grpc.UnaryInterceptor(
			middleware.ChainUnaryServer(s.unaryInterceptors...),
		))
	}
	if len(s.streamInterceptors) > 0 {
		grpcOpts = append(grpcOpts, grpc.StreamInterceptor(
			middleware.ChainStreamServer(s.streamInterceptors...),
		))
	}

	// 添加默认选项
	grpcOpts = append(grpcOpts, grpc.StatsHandler(otelgrpc.NewServerHandler()))

	s.Server = grpc.NewServer(grpcOpts...)
	if handler != nil {
		handler(s.Server)
	}
	return s
}

// Server ..
type Server struct {
	id                 string
	name               string
	metadata           map[string]string
	unaryInterceptors  []grpc.UnaryServerInterceptor
	streamInterceptors []grpc.StreamServerInterceptor

	addr     string
	grpcOpts []grpc.ServerOption
	Server   *grpc.Server

	// 新增：服务发现相关字段
	serviceDiscovery *ServiceDiscovery
	autoDiscover     bool
}

// Start ..
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.addr = ln.Addr().String() //确保随机端口时候 s.addr 值的正确性

	// 如果启用了自动服务发现
	if s.autoDiscover && s.serviceDiscovery != nil {
		// 发现已注册的服务
		if err := s.serviceDiscovery.DiscoverServices(s.addr, s.metadata); err != nil {
			logger.Error("service discovery failed", "error", err)
		} else {
			// 注册所有发现的服务
			go func() {
				if err := s.serviceDiscovery.RegisterAllServices(ctx); err != nil {
					logger.Error("register discovered services failed", "error", err)
				}
			}()
		}
	}

	go func() {
		logger.Info("grpc server serve", "addr", s.addr)
		if err := s.Server.Serve(ln); err != nil {
			if err != grpc.ErrServerStopped {
				log.Fatalf("grpc server serve failed: %s\n", err)
			}
			return
		}
	}()
	<-ctx.Done()
	logger.Info("grpc server stopped...")
	s.Server.GracefulStop()
	return nil
}

// String ..
func (s *Server) String() string {
	return addr.ParseHostPort(s.addr)
}

func (s *Server) ID() string {
	return s.id
}
func (s *Server) Name() string {
	return s.name
}

func (s *Server) Kind() string {
	return "grpc"
}

func (s *Server) Addr() string {
	return addr.ParseHostPort(s.addr)
}

func (s *Server) Metadata() map[string]string {
	return s.metadata
}
