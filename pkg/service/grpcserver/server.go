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

// ServiceDiscovery 从gRPC Server中读取已注册的服务
type ServiceDiscovery struct {
	server     *grpc.Server
	registries []discover.Registry
	services   map[string]*ProtoServiceInfo
}

// ProtoServiceInfo protobuf服务信息
type ProtoServiceInfo struct {
	ServiceName string            `json:"service_name"`
	Methods     []string          `json:"methods"`
	Metadata    map[string]string `json:"metadata"`
	ServerAddr  string            `json:"server_addr"`
}

// NewServiceDiscovery 创建服务发现器
func NewServiceDiscovery(server *grpc.Server, registries ...discover.Registry) *ServiceDiscovery {
	return &ServiceDiscovery{
		server:     server,
		registries: registries,
		services:   make(map[string]*ProtoServiceInfo),
	}
}

// DiscoverServices 发现gRPC Server中已注册的服务
func (sd *ServiceDiscovery) DiscoverServices(serverAddr string, baseMetadata map[string]string) error {
	// 使用gRPC内置的GetServiceInfo方法获取服务信息
	serviceInfos := sd.server.GetServiceInfo()

	for serviceName, serviceInfo := range serviceInfos {
		methods := make([]string, 0, len(serviceInfo.Methods))
		for _, method := range serviceInfo.Methods {
			methods = append(methods, method.Name)
		}

		// 合并元数据
		metadata := make(map[string]string)
		for k, v := range baseMetadata {
			metadata[k] = v
		}
		metadata["kind"] = "grpc"
		metadata["methods"] = fmt.Sprintf("%v", methods)
		if serviceInfo.Metadata != nil {
			metadata["proto_file"] = serviceInfo.Metadata.(string) // 包含proto文件信息
		}

		protoService := &ProtoServiceInfo{
			ServiceName: serviceName,
			Methods:     methods,
			Metadata:    metadata,
			ServerAddr:  serverAddr,
		}

		sd.services[serviceName] = protoService

		logger.Info("discovered gRPC service",
			"service", serviceName,
			"methods", methods,
			"proto_file", serviceInfo.Metadata)
	}

	return nil
}

// RegisterAllServices 注册所有发现的服务
func (sd *ServiceDiscovery) RegisterAllServices(ctx context.Context) error {
	for serviceName, serviceInfo := range sd.services {
		go func(name string, info *ProtoServiceInfo) {
			// 为每个protobuf服务创建独立的注册实例
			serviceWrapper := &ProtoServiceWrapper{
				id:          uuid.New(),
				serviceName: name,
				methods:     info.Methods,
				addr:        info.ServerAddr,
				metadata:    info.Metadata,
			}

			// 注册到所有注册中心
			for _, registry := range sd.registries {
				go func(r discover.Registry) {
					stop, err := r.Register(ctx, serviceWrapper)
					if err != nil {
						logger.Error("proto service register error",
							"service", name,
							"error", err)
						return
					}
					defer stop()
					<-ctx.Done()
				}(registry)
			}
		}(serviceName, serviceInfo)
	}
	return nil
}

// ProtoServiceWrapper 实现discover.Service接口
type ProtoServiceWrapper struct {
	id          string
	serviceName string
	methods     []string
	addr        string
	metadata    map[string]string
}

func (w *ProtoServiceWrapper) ID() string {
	return w.id
}

func (w *ProtoServiceWrapper) Name() string {
	return w.serviceName
}

func (w *ProtoServiceWrapper) Kind() string {
	return "grpc"
}

func (w *ProtoServiceWrapper) Addr() string {
	return w.addr
}

func (w *ProtoServiceWrapper) Metadata() map[string]string {
	return w.metadata
}
