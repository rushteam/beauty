package grpcserver

import (
	"context"
	"log"
	"net"

	"github.com/rushteam/beauty/pkg/logger"
	"google.golang.org/grpc"
)

// New create a web service with the name
func New(addr string) *Server {
	s := &Server{
		Addr:   addr,
		Server: grpc.NewServer(),
	}
	// if conf, err := config.New(config.Env(), name); err == nil {
	// 	s.Mode = conf.GetString(ServiceKind + ".mode")
	// 	s.Addr = conf.GetString(ServiceKind + ".addr")
	// } else {
	// 	log.Warn("no config file...", zap.String("kind", ServiceKind), zap.String("name", name))
	// }
	return s
}

// Server ..
type Server struct {
	Mode   string
	Addr   string
	Server *grpc.Server
}

// Start ..
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return err
	}
	s.Addr = ln.Addr().String() //确保随机端口时候 s.Addr 值的正确性
	go func() {
		logger.Info("grpc server serve", "addr", s.Addr)
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
	return "grpc"
}
