package webserver

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"time"

	"github.com/rushteam/beauty/pkg/service/discover"
	"github.com/rushteam/beauty/pkg/service/logger"
	"github.com/rushteam/beauty/pkg/utils/addr"
	"github.com/rushteam/beauty/pkg/utils/uuid"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

var _ discover.Service = (*Server)(nil)

type Option func(*Server)

func WithServiceName(name string) Option {
	return func(s *Server) {
		s.name = name
	}
}

// WithShutdownTimeout 设置 HTTP 服务优雅关闭的最长等待时间，默认 30s。
func WithShutdownTimeout(d time.Duration) Option {
	return func(s *Server) {
		s.shutdownTimeout = d
	}
}

func WithReadTimeout(d time.Duration) Option {
	return func(s *Server) {
		s.readTimeout = d
	}
}

func WithWriteTimeout(d time.Duration) Option {
	return func(s *Server) {
		s.writeTimeout = d
	}
}

func WithIdleTimeout(d time.Duration) Option {
	return func(s *Server) {
		s.idleTimeout = d
	}
}

// WithTLS 通过证书文件启用 HTTPS。certFile 和 keyFile 为 PEM 格式文件路径。
func WithTLS(certFile, keyFile string) Option {
	return func(s *Server) {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			panic(fmt.Sprintf("webserver: failed to load TLS key pair: %v", err))
		}
		if s.Server.TLSConfig == nil {
			s.Server.TLSConfig = &tls.Config{}
		}
		s.Server.TLSConfig.Certificates = append(s.Server.TLSConfig.Certificates, cert)
	}
}

// WithTLSConfig 通过自定义 tls.Config 启用 HTTPS，适合 mTLS 或自定义 CA 场景。
func WithTLSConfig(cfg *tls.Config) Option {
	return func(s *Server) {
		s.Server.TLSConfig = cfg
	}
}

func WithMetadata(md map[string]string) Option {
	return func(s *Server) {
		maps.Copy(s.metadata, md)
	}
}

// WithMiddleware 添加 HTTP 中间件
func WithMiddleware(middlewares ...func(http.Handler) http.Handler) Option {
	return func(s *Server) {
		if s.middlewares == nil {
			s.middlewares = make([]func(http.Handler) http.Handler, 0)
		}
		s.middlewares = append(s.middlewares, middlewares...)
	}
}

func New(addr string, mux http.Handler, opts ...Option) *Server {
	s := &Server{
		id:              uuid.New(),
		name:            "http-server",
		metadata:        map[string]string{"kind": "http"},
		middlewares:     make([]func(http.Handler) http.Handler, 0),
		ready:           make(chan struct{}),
		shutdownTimeout: 30 * time.Second,
		readTimeout:     30 * time.Second,
		writeTimeout:    30 * time.Second,
		idleTimeout:     90 * time.Second,
		Server:          &http.Server{},
	}

	// 应用选项
	for _, o := range opts {
		o(s)
	}

	// 应用中间件（从外到内的顺序）
	handler := mux
	for i := len(s.middlewares) - 1; i >= 0; i-- {
		handler = s.middlewares[i](handler)
	}
	// 最外层包裹 OTel HTTP 追踪
	s.Server.Addr = addr
	s.Server.Handler = otelhttp.NewHandler(handler, s.name)
	s.Server.ReadTimeout = s.readTimeout
	s.Server.WriteTimeout = s.writeTimeout
	s.Server.IdleTimeout = s.idleTimeout

	return s
}

type Server struct {
	id              string
	name            string
	metadata        map[string]string
	middlewares     []func(http.Handler) http.Handler
	ready           chan struct{}
	shutdownTimeout time.Duration
	readTimeout     time.Duration
	writeTimeout    time.Duration
	idleTimeout     time.Duration

	*http.Server
}

func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.Server.Addr)
	if err != nil {
		return err
	}
	s.Server.Addr = ln.Addr().String()
	close(s.ready)
	go func() {
		logger.Info("web server serve", slog.String("addr", s.Server.Addr))
		var serveErr error
		if s.Server.TLSConfig != nil {
			serveErr = s.Serve(tls.NewListener(ln, s.Server.TLSConfig))
		} else {
			serveErr = s.Serve(ln)
		}
		if serveErr != nil && serveErr != http.ErrServerClosed {
			logger.Error("web server serve failed", "error", serveErr)
		}
	}()
	<-ctx.Done()
	logger.Info("web server stopping...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
	defer cancel()
	if err := s.Shutdown(shutdownCtx); err != nil {
		logger.Error("web server shutdown error", "error", err)
		return err
	}
	logger.Info("web server stopped")
	return nil
}

func (s *Server) Ready() <-chan struct{} {
	return s.ready
}

func (s *Server) String() string {
	return addr.ParseHostPort(s.Server.Addr)
}

func (s *Server) ID() string {
	return s.id
}
func (s *Server) Name() string {
	return s.name
}

func (s *Server) Kind() string {
	return "http"
}

func (s *Server) Addr() string {
	return addr.ParseHostPort(s.Server.Addr)
}

func (s *Server) Metadata() map[string]string {
	return s.metadata
}
