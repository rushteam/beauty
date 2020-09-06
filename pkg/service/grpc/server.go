package grpc

import (
	"context"
	"net"

	"github.com/rushteam/beauty/pkg/config"
	"github.com/rushteam/beauty/pkg/log"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

//ServiceKind ..
const ServiceKind = "grpc"

//Build create a web service with the name
func Build(name string) (*Server, error) {
	s := &Server{
		Name:   name,
		Addr:   ":50000",
		Server: grpc.NewServer(),
	}
	if conf, err := config.New(config.Env(), name); err == nil {
		s.Mode = conf.GetString(ServiceKind + ".mode")
		s.Addr = conf.GetString(ServiceKind + ".addr")
	} else {
		log.Warn("no config file...", zap.String("kind", ServiceKind), zap.String("name", name))
	}
	return s, nil
}

//Server ..
type Server struct {
	Name   string
	Mode   string
	Addr   string
	Server *grpc.Server
}

//Start ..
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return err
	}
	s.Addr = ln.Addr().String() //确保随机端口时候 s.Addr 值的正确性
	if err := s.Server.Serve(ln); err != nil {
		return err
	}
	return nil
}

//Stop ..
func (s *Server) Stop(ctx context.Context) error {
	closed := make(chan struct{})
	go func() {
		s.Server.GracefulStop()
		close(closed)
	}()
	select {
	case <-ctx.Done():
		//超时强制结束
		if ctx.Err() != nil {
			s.Server.Stop()
		}
		return nil
	case <-closed:
		//正常结束
		return nil
	}
}

//String ..
func (s *Server) String() string {
	return ServiceKind + "." + s.Name
}
