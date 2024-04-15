package webserver

import (
	"context"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/rushteam/beauty/pkg/addr"
	"github.com/rushteam/beauty/pkg/discover"
	"github.com/rushteam/beauty/pkg/logger"
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

func New(addr string, mux http.Handler, opts ...Options) *Server {
	s := &Server{
		id:       uuid.New(),
		name:     "http-server",
		metadata: map[string]string{"kind": "http"},
		Server: &http.Server{
			Addr:    addr,
			Handler: mux,
		},
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

type Server struct {
	id       string
	name     string
	metadata map[string]string

	*http.Server
}

func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.Server.Addr)
	if err != nil {
		return err
	}
	s.Server.Addr = ln.Addr().String()
	go func() {
		logger.Info("web server serve", "addr", s.Server.Addr)
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
