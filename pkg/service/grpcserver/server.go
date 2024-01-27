package grpcserver

import (
	"context"
	"log"
	"net"

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
		log.Println("grpc server serve", s.Addr)
		if err := s.Server.Serve(ln); err != nil {
			log.Fatalf("grpc server serve failed: %s\n", err)
			// log.Println("grpc server serve failed", err)
			// slog.Error("grpc server serve failed", slog.Any("error", err))
			return
		}
	}()
	<-ctx.Done()
	log.Println("grpc server stopped...")
	s.Server.GracefulStop()
	return nil
}

// String ..
func (s *Server) String() string {
	return "grpc"
}
