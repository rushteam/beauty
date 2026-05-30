package grpcserver

import (
	"context"
	"crypto/tls"
	"fmt"
	"maps"
	"net"
	"time"

	middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/service/logger"
	"github.com/rushteam/beauty/pkg/utils/addr"
	"github.com/rushteam/beauty/pkg/utils/uuid"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
)

var _ discover.Service = (*Server)(nil)

func WithGrpcServerOptions(opts ...grpc.ServerOption) Option {
	return func(s *Server) {
		s.grpcOpts = append(s.grpcOpts, opts...)
	}
}

func WithGrpcServerUnaryInterceptor(interceptors ...grpc.UnaryServerInterceptor) Option {
	return WithGrpcServerOptions(grpc.UnaryInterceptor(
		middleware.ChainUnaryServer(interceptors...),
	))
}

func WithGrpcServerStreamInterceptor(interceptors ...grpc.StreamServerInterceptor) Option {
	return WithGrpcServerOptions(grpc.StreamInterceptor(
		middleware.ChainStreamServer(interceptors...),
	))
}

func WithServiceName(name string) Option {
	return func(s *Server) {
		s.name = name
	}
}

func WithMetadata(md map[string]string) Option {
	return func(s *Server) {
		maps.Copy(s.metadata, md)
	}
}

// WithAutoServiceDiscovery 启用自动服务发现，为每个protobuf服务单独注册。
// 可通过 sdOpts 传入 WithInternalServices() 等服务发现选项。
func WithAutoServiceDiscovery(registries []discover.Registry, sdOpts ...ServiceDiscoveryOption) Option {
	return func(s *Server) {
		s.autoDiscover = true
		s.serviceDiscovery = NewServiceDiscovery(s.Server, registries, sdOpts...)
	}
}

// WithRegionInfo 设置地域信息，兼容Polaris
func WithRegionInfo(region, zone, campus string) Option {
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
func WithEnvironment(env string) Option {
	return func(s *Server) {
		if s.metadata == nil {
			s.metadata = make(map[string]string)
		}
		s.metadata["environment"] = env
	}
}

// WithWeight 设置服务权重
func WithWeight(weight int) Option {
	return func(s *Server) {
		if s.metadata == nil {
			s.metadata = make(map[string]string)
		}
		s.metadata["weight"] = fmt.Sprintf("%d", weight)
	}
}

// WithPriority 设置服务优先级
func WithPriority(priority int) Option {
	return func(s *Server) {
		if s.metadata == nil {
			s.metadata = make(map[string]string)
		}
		s.metadata["priority"] = fmt.Sprintf("%d", priority)
	}
}

type Option func(*Server)

// WithTLS 通过证书文件启用 TLS。certFile 和 keyFile 为 PEM 格式文件路径。
func WithTLS(certFile, keyFile string) Option {
	return func(s *Server) {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			panic(fmt.Sprintf("grpcserver: failed to load TLS key pair: %v", err))
		}
		creds := credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{cert}})
		s.grpcOpts = append(s.grpcOpts, grpc.Creds(creds))
	}
}

// WithTLSConfig 通过自定义 tls.Config 启用 TLS，适合需要 mTLS 或自定义 CA 的场景。
func WithTLSConfig(cfg *tls.Config) Option {
	return func(s *Server) {
		s.grpcOpts = append(s.grpcOpts, grpc.Creds(credentials.NewTLS(cfg)))
	}
}

// WithGracefulStopTimeout 设置 GracefulStop 的最长等待时间，超时后强制 Stop。
// 默认 30s。
func WithGracefulStopTimeout(d time.Duration) Option {
	return func(s *Server) {
		s.gracefulStopTimeout = d
	}
}

// New create a grpc service with the name
func New(addr string, handler func(*grpc.Server), opts ...Option) *Server {
	s := &Server{
		id:                  uuid.New(),
		name:                "grpc-server",
		metadata:            map[string]string{"kind": "grpc"},
		unaryInterceptors:   make([]grpc.UnaryServerInterceptor, 0),
		streamInterceptors:  make([]grpc.StreamServerInterceptor, 0),
		addr:                addr,
		ready:               make(chan struct{}),
		gracefulStopTimeout: 30 * time.Second,
		Server:              nil,
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
	grpcOpts = append(grpcOpts,
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle:     time.Minute * 5,
			MaxConnectionAge:      time.Minute * 30,
			MaxConnectionAgeGrace: time.Second * 10,
			Time:                  time.Second * 20,
			Timeout:               time.Second * 10,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             time.Second * 10,
			PermitWithoutStream: true,
		}),
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
	)

	s.Server = grpc.NewServer(grpcOpts...)

	// 自动注册 gRPC Health Check 服务
	s.healthServer = health.NewServer()
	healthpb.RegisterHealthServer(s.Server, s.healthServer)
	s.healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)

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

	addr                string
	ready               chan struct{}
	grpcOpts            []grpc.ServerOption
	gracefulStopTimeout time.Duration
	Server              *grpc.Server
	healthServer        *health.Server

	// 服务发现相关字段
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
	close(s.ready)

	// 如果启用了自动服务发现
	var waitRegistrations func()
	if s.autoDiscover && s.serviceDiscovery != nil {
		if err := s.serviceDiscovery.DiscoverServices(s.addr, s.metadata); err != nil {
			logger.Error("service discovery failed", "error", err)
		} else {
			wait, err := s.serviceDiscovery.RegisterAllServices(ctx)
			if err != nil {
				logger.Error("register discovered services failed", "error", err)
			} else {
				waitRegistrations = wait
			}
		}
	}

	go func() {
		logger.Info("grpc server serve", "addr", s.addr)
		if err := s.Server.Serve(ln); err != nil && err != grpc.ErrServerStopped {
			logger.Error("grpc server serve failed", "error", err)
		}
	}()
	<-ctx.Done()
	logger.Info("grpc server stopping...")
	s.healthServer.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	stopped := make(chan struct{})
	go func() {
		s.Server.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(s.gracefulStopTimeout):
		logger.Warn("grpc graceful stop timeout, forcing stop",
			"timeout", s.gracefulStopTimeout)
		s.Server.Stop()
		<-stopped
	}
	logger.Info("grpc server stopped")
	if waitRegistrations != nil {
		waitRegistrations()
	}
	return nil
}

func (s *Server) Ready() <-chan struct{} {
	return s.ready
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
