// Package graphql 提供基于 gqlgen (schema-first) 的 GraphQL/BFF 服务集成,
// 作为独立 Go 模块发布 (github.com/rushteam/beauty/contrib/graphql),封装为
// beauty.Service 一等公民。包含 DataLoader、认证透传、复杂度限制、持久化查询、
// Federation、Subscription 等完整 BFF 能力。
package graphql

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/99designs/gqlgen/graphql/playground"
)

// Server 是 GraphQL 服务,实现 beauty.Service + discover.Service 接口。
type Server struct {
	addr    string
	name    string
	id      string
	version string

	es         graphql.ExecutableSchema
	srv        *handler.Server
	httpSrv    *http.Server
	mux        *http.ServeMux
	middleware []func(http.Handler) http.Handler
	extensions []graphql.HandlerExtension

	playgroundEnabled bool
	playgroundPath    string
	graphqlPath       string

	// subscription transports
	transports []graphql.Transport

	shutdownTimeout time.Duration
	ready           chan struct{}
	readyOnce       sync.Once
}

// New 创建 GraphQL 服务。es 是 gqlgen 生成的 ExecutableSchema。
func New(addr string, es graphql.ExecutableSchema, opts ...Option) *Server {
	s := &Server{
		addr:            addr,
		name:            "graphql",
		es:              es,
		playgroundPath:  "/",
		graphqlPath:     "/query",
		shutdownTimeout: 30 * time.Second,
		ready:           make(chan struct{}),
	}
	for _, o := range opts {
		o(s)
	}
	s.build()
	return s
}

func (s *Server) build() {
	s.srv = handler.New(s.es)

	// Default transports
	s.srv.AddTransport(transport.Options{})
	s.srv.AddTransport(transport.GET{})
	s.srv.AddTransport(transport.POST{})
	s.srv.AddTransport(transport.MultipartForm{})

	// Custom transports (WS/SSE subscription)
	for _, t := range s.transports {
		s.srv.AddTransport(t)
	}

	// Introspection (always enabled; disable via extension if needed)
	s.srv.Use(extension.Introspection{})

	// User extensions
	for _, ext := range s.extensions {
		s.srv.Use(ext)
	}

	// Recovery
	s.srv.SetRecoverFunc(func(ctx context.Context, err interface{}) error {
		slog.Error("graphql: resolver panic", "panic", err)
		return fmt.Errorf("internal server error")
	})

	// Build HTTP mux
	s.mux = http.NewServeMux()
	s.mux.Handle(s.graphqlPath, s.srv)

	if s.playgroundEnabled {
		s.mux.Handle(s.playgroundPath, playground.Handler("GraphQL Playground", s.graphqlPath))
	}
}

// Start 实现 beauty.Service。
func (s *Server) Start(ctx context.Context) error {
	var h http.Handler = s.mux
	for i := len(s.middleware) - 1; i >= 0; i-- {
		h = s.middleware[i](h)
	}

	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("graphql: listen %s: %w", s.addr, err)
	}

	s.addr = ln.Addr().String()
	s.httpSrv = &http.Server{Handler: h}

	s.readyOnce.Do(func() { close(s.ready) })

	errCh := make(chan error, 1)
	go func() {
		if err := s.httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	slog.Info("graphql: serving", "addr", s.addr, "playground", s.playgroundEnabled)

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
		defer cancel()
		return s.httpSrv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

// Ready 实现 beauty.ReadyNotifier。
func (s *Server) Ready() <-chan struct{} { return s.ready }

// String 实现 beauty.Service。
func (s *Server) String() string { return "graphql.Server(" + s.name + ")" }

// ID 实现 discover.Service。
func (s *Server) ID() string { return s.id }

// Name 实现 discover.Service。
func (s *Server) Name() string { return s.name }

// Kind 实现 discover.Service。
func (s *Server) Kind() string { return "http" }

// Addr 实现 discover.Service。
func (s *Server) Addr() string { return s.addr }

// Metadata 实现 discover.Service。
func (s *Server) Metadata() map[string]string {
	m := map[string]string{"kind": "http", "protocol": "graphql"}
	if s.version != "" {
		m["version"] = s.version
	}
	return m
}

// Handler 返回内部的 gqlgen handler.Server(供需要高级配置时使用)。
func (s *Server) Handler() *handler.Server { return s.srv }
