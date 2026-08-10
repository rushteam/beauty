// Package connectrpc 提供 Connect (connectrpc.com/connect) 协议的一等公民集成。
//
// Connect 是基于标准 net/http 的 Protobuf RPC 框架，同时支持 Connect、gRPC 和
// gRPC-Web 三种线上协议。与 grpcserver 不同，Connect handler 就是 http.Handler，
// 天然兼容所有 HTTP 中间件和基础设施。
//
// 服务端通过 New 创建 Server，用 Handle 注册 protoc-gen-connect-go 生成的 handler，
// 然后传入 beauty.WithService 即可纳入框架生命周期管理：
//
//	srv := connectrpc.New(":8080")
//	srv.Handle(pingv1connect.NewPingServiceHandler(&PingServer{}))
//	app := beauty.New(beauty.WithService(srv))
//
// 客户端通过 NewTransport 创建支持服务发现的 http.RoundTripper，配合
// protoc-gen-connect-go 生成的 Client 使用：
//
//	rt := connectrpc.NewTransport(discovery, "my.service.v1.MyService")
//	client := pingv1connect.NewPingServiceClient(&http.Client{Transport: rt}, "http://my.service.v1.MyService/")
package connectrpc

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"connectrpc.com/grpchealth"
	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/service/logger"
	"github.com/rushteam/beauty/pkg/utils/addr"
	"github.com/rushteam/beauty/pkg/utils/uuid"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

var _ discover.Service = (*Server)(nil)

// Server 是基于 Connect 协议的 HTTP 服务，实现了 beauty.Service 和
// discover.Service 接口，可直接传入 beauty.WithService 使用。
//
// 默认启用 H2C（HTTP/2 Cleartext），以支持 gRPC 协议；默认注册
// gRPC 健康检查端点（grpc.health.v1.Health）。
type Server struct {
	id       string
	name     string
	metadata map[string]string

	addr            string
	ready           chan struct{}
	shutdownTimeout time.Duration
	readTimeout     time.Duration
	writeTimeout    time.Duration
	idleTimeout     time.Duration

	mux         *http.ServeMux
	middlewares []func(http.Handler) http.Handler

	enableH2C    bool
	enableHealth bool
	tlsConfig    *tls.Config

	serviceNames []string

	registries       []discover.Registry
	autoDiscover     bool
	serviceDiscovery *ServiceDiscovery
}

// New 创建 Connect 服务。addr 为监听地址（如 ":8080"），支持 ":0" 随机端口。
// 默认启用 H2C 和健康检查。
func New(addr string, opts ...Option) *Server {
	s := &Server{
		id:              uuid.New(),
		name:            "connect-server",
		metadata:        map[string]string{"kind": "connect"},
		addr:            addr,
		ready:           make(chan struct{}),
		mux:             http.NewServeMux(),
		middlewares:     make([]func(http.Handler) http.Handler, 0),
		serviceNames:    make([]string, 0),
		shutdownTimeout: 30 * time.Second,
		enableH2C:       true,
		enableHealth:    true,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Handle 注册 Connect handler。path 和 handler 通常由 protoc-gen-connect-go
// 生成的 NewXxxServiceHandler 函数返回。框架会自动从 path 解析 protobuf
// 服务全限定名，用于健康检查和服务发现注册。
func (s *Server) Handle(path string, handler http.Handler) {
	s.mux.Handle(path, handler)
	if svcName := parseServiceName(path); svcName != "" {
		s.serviceNames = append(s.serviceNames, svcName)
	}
}

// HandleFunc 注册普通 HTTP handler（非 Connect 路由）。
func (s *Server) HandleFunc(pattern string, handler http.HandlerFunc) {
	s.mux.HandleFunc(pattern, handler)
}

// Start 启动服务，阻塞直到 ctx 取消后优雅关闭。实现 beauty.Service 接口。
func (s *Server) Start(ctx context.Context) error {
	var readyOnce sync.Once
	signalReady := func() { readyOnce.Do(func() { close(s.ready) }) }
	defer signalReady()

	if s.enableHealth {
		checker := grpchealth.NewStaticChecker(s.serviceNames...)
		s.mux.Handle(grpchealth.NewHandler(checker))
	}

	var handler http.Handler = s.mux
	for i := len(s.middlewares) - 1; i >= 0; i-- {
		handler = s.middlewares[i](handler)
	}
	handler = otelhttp.NewHandler(handler, s.name)

	server := &http.Server{
		Handler:      handler,
		ReadTimeout:  s.readTimeout,
		WriteTimeout: s.writeTimeout,
		IdleTimeout:  s.idleTimeout,
	}
	if s.enableH2C && s.tlsConfig == nil {
		p := new(http.Protocols)
		p.SetHTTP1(true)
		p.SetUnencryptedHTTP2(true)
		server.Protocols = p
	}
	if s.tlsConfig != nil {
		server.TLSConfig = s.tlsConfig
	}

	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.addr = ln.Addr().String()
	signalReady()

	var waitRegistrations func()
	if s.autoDiscover && s.serviceDiscovery != nil {
		s.serviceDiscovery.DiscoverServices(s.addr, s.metadata, s.serviceNames)
		wait, regErr := s.serviceDiscovery.RegisterAllServices(ctx)
		if regErr != nil {
			logger.Error("connect register discovered services failed", "error", regErr)
		} else {
			waitRegistrations = wait
		}
	}

	go func() {
		logger.Info("connect server serve", "addr", s.addr)
		var serveErr error
		if server.TLSConfig != nil {
			serveErr = server.Serve(tls.NewListener(ln, server.TLSConfig))
		} else {
			serveErr = server.Serve(ln)
		}
		if serveErr != nil && serveErr != http.ErrServerClosed {
			logger.Error("connect server serve failed", "error", serveErr)
		}
	}()

	<-ctx.Done()
	logger.Info("connect server stopping...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("connect server shutdown error", "error", err)
		return err
	}
	logger.Info("connect server stopped")
	if waitRegistrations != nil {
		waitRegistrations()
	}
	return nil
}

// Ready 返回一个 channel，在服务端口就绪后关闭。实现 ReadyNotifier 接口。
func (s *Server) Ready() <-chan struct{} {
	return s.ready
}

func (s *Server) String() string {
	return addr.ParseHostPort(s.addr)
}

func (s *Server) ID() string                  { return s.id }
func (s *Server) Name() string                { return s.name }
func (s *Server) Kind() string                { return "connect" }
func (s *Server) Addr() string                { return addr.ParseHostPort(s.addr) }
func (s *Server) Metadata() map[string]string { return s.metadata }

// parseServiceName 从 Connect handler 路径解析 protobuf 服务全限定名。
// 例如 "/acme.user.v1.UserService/" -> "acme.user.v1.UserService"
func parseServiceName(path string) string {
	name := strings.Trim(path, "/")
	if name == "" || !strings.Contains(name, ".") {
		return ""
	}
	return name
}
