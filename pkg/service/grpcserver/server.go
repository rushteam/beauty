package grpcserver

import (
	"context"
	"log"
	"net"

	"github.com/rushteam/beauty/pkg/addr"
	"github.com/rushteam/beauty/pkg/discover"
	"github.com/rushteam/beauty/pkg/logger"
	"github.com/rushteam/beauty/pkg/uuid"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
)

var _ discover.Service = (*Server)(nil)

func WithGrpcServerOptions(opts ...grpc.ServerOption) Options {
	return func(s *Server) {
		s.grpcOpts = append(s.grpcOpts, opts...)
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

type Options func(*Server)

// New create a grpc service with the name
func New(addr string, handler func(*grpc.Server), opts ...Options) *Server {
	s := &Server{
		id:       uuid.New(),
		name:     "grpc-server",
		metadata: make(map[string]string, 0),
		addr:     addr,
		Server: grpc.NewServer(
			grpc.StatsHandler(otelgrpc.NewServerHandler()),
		),
	}
	for _, o := range opts {
		o(s)
	}
	s.grpcOpts = append(s.grpcOpts,
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
	)
	s.Server = grpc.NewServer(s.grpcOpts...)
	if handler != nil {
		handler(s.Server)
	}
	return s
}

// Server ..
type Server struct {
	id       string
	name     string
	metadata map[string]string

	addr     string
	grpcOpts []grpc.ServerOption
	Server   *grpc.Server
}

// Start ..
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.addr = ln.Addr().String() //确保随机端口时候 s.addr 值的正确性
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
	return s.addr
}

func (s *Server) Metadata() map[string]string {
	return s.metadata
}
