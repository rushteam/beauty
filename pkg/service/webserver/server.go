package webserver

import (
	"context"
	"log"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/rushteam/beauty/pkg/addr"
	"github.com/rushteam/beauty/pkg/circuitbreaker"
	"github.com/rushteam/beauty/pkg/discover"
	"github.com/rushteam/beauty/pkg/logger"
	"github.com/rushteam/beauty/pkg/timeout"
	"github.com/rushteam/beauty/pkg/uuid"
)

var _ discover.Service = (*Server)(nil)

type Options func(*Server)

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

// WithMiddleware 添加 HTTP 中间件
func WithMiddleware(middlewares ...func(http.Handler) http.Handler) Options {
	return func(s *Server) {
		if s.middlewares == nil {
			s.middlewares = make([]func(http.Handler) http.Handler, 0)
		}
		s.middlewares = append(s.middlewares, middlewares...)
	}
}

// WithCircuitBreaker 添加熔断器中间件
func WithCircuitBreaker(cb *circuitbreaker.CircuitBreaker) Options {
	return WithMiddleware(circuitbreaker.HTTPMiddleware(cb))
}

// WithTimeout 添加超时控制中间件
func WithTimeout(tc *timeout.TimeoutController) Options {
	return WithMiddleware(timeout.HTTPMiddleware(tc))
}

func New(addr string, mux http.Handler, opts ...Options) *Server {
	s := &Server{
		id:          uuid.New(),
		name:        "http-server",
		metadata:    map[string]string{"kind": "http"},
		middlewares: make([]func(http.Handler) http.Handler, 0),
		Server: &http.Server{
			Addr:    addr,
			Handler: mux,
		},
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
	s.Server.Handler = handler

	return s
}

type Server struct {
	id          string
	name        string
	metadata    map[string]string
	middlewares []func(http.Handler) http.Handler

	*http.Server
}

func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.Server.Addr)
	if err != nil {
		return err
	}
	s.Server.Addr = ln.Addr().String()
	go func() {
		logger.Info("web server serve", slog.String("addr", s.Server.Addr))
		if err := s.Serve(ln); err != nil {
			if err != http.ErrServerClosed {
				log.Fatalf("web server listen failed: %s\n", err)
			}
		}
	}()
	<-ctx.Done()
	logger.Info("web server stopped...")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	return s.Shutdown(ctx)
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
