package grpcserver

import (
	"context"
	"log"
	"net"

	"github.com/rushteam/beauty/pkg/addr"
	"github.com/rushteam/beauty/pkg/logger"
	"google.golang.org/grpc"
)

// New create a web service with the name
func New(addr string, handler func(*grpc.Server)) *Server {
	s := &Server{
		Addr:   addr,
		Server: grpc.NewServer(),
	}
	if handler != nil {
		handler(s.Server)
	}
	return s
}

// Server ..
type Server struct {
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
	// time.Sleep(time.Second)
	// fmt.Println("GetServiceInfo", s.Server.GetServiceInfo())
	<-ctx.Done()
	logger.Info("grpc server stopped...")
	s.Server.GracefulStop()
	return nil
}

// String ..
func (s *Server) String() string {
	return addr.ParseHostPort(s.Addr)
}
