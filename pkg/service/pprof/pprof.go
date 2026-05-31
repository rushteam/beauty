package pprof

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	_ "net/http/pprof" // 注册 /debug/pprof/* 路由到 DefaultServeMux
	"time"
)

const defaultAddr = "127.0.0.1:6060"

// Server 是一个独立的 pprof HTTP 服务，实现 beauty.Service 接口。
// 默认只监听 loopback，避免意外暴露到公网。
type Server struct {
	addr   string
	server *http.Server
}

type Option func(*Server)

// WithAddr 覆盖监听地址，默认 127.0.0.1:6060。
// 生产环境如需远程访问，建议通过 SSH 隧道而非直接对外暴露。
func WithAddr(addr string) Option {
	return func(s *Server) {
		s.addr = addr
	}
}

// New 创建 pprof Server。通过 beauty.WithService(pprof.New()) 挂载到应用。
func New(opts ...Option) *Server {
	s := &Server{addr: defaultAddr}
	for _, o := range opts {
		o(s)
	}
	s.server = &http.Server{
		Addr:        s.addr,
		Handler:     http.DefaultServeMux,
		ReadTimeout: 30 * time.Second,
		// WriteTimeout 不设置：pprof 的 profile/trace 采集需要长时间持有连接
	}
	return s
}

func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.addr = ln.Addr().String()
	slog.Info("pprof server listening", "addr", s.addr)

	go func() {
		if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("pprof server error", "err", err)
		}
	}()

	<-ctx.Done()
	shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return s.server.Shutdown(shutCtx)
}

func (s *Server) String() string { return "pprof@" + s.addr }
