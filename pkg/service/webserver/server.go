package webserver

import (
	"context"
	"log"
	"net"
	"net/http"

	"github.com/rushteam/beauty/pkg/addr"
	"github.com/rushteam/beauty/pkg/logger"
)

func New(addr string, mux http.Handler) *Server {
	return &Server{
		Mux:  mux,
		Addr: addr,
	}
}

type Server struct {
	Addr string
	Mux  http.Handler
}

func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:    s.Addr,
		Handler: s.Mux,
	}
	go func() {
		logger.Info("web server serve", "addr", s.Addr)
		if err := server.Serve(ln); err != nil {
			if err != http.ErrServerClosed {
				log.Fatalf("web server listen failed: %s\n", err)
			}
		}
	}()
	<-ctx.Done()
	logger.Info("web server stopped...")
	return server.Shutdown(ctx)
}

func (s *Server) String() string {
	host, port, err := net.SplitHostPort(s.Addr)
	if err != nil {
		logger.Error("web server addr error", err)
	}
	return addr.Parse(host) + ":" + port
}
