package web

import (
	"context"
	"net"
	"net/http"
	"path/filepath"

	"github.com/gin-gonic/gin"
)

//ServiceKind ..
const ServiceKind = "webserver"

//New new a WebServer with the name
func New(name string) (*WebServer, error) {
	ctx, cancel := context.WithCancel(context.Background())
	x := gin.New()
	x.Use(recoverMiddleware())
	s := &WebServer{
		name:   name,
		ctx:    ctx,
		cancel: cancel,
		Mode:   gin.DebugMode,
		Engine: x,
		Server: &http.Server{
			Handler: x,
			// Addr:    ":http",
		},
	}
	if len(s.Mode) > 0 {
		gin.SetMode(s.Mode)
	}
	return s, nil
}

//WebServer ..
type WebServer struct {
	name   string
	ctx    context.Context
	cancel context.CancelFunc

	*gin.Engine
	*http.Server
	Mode string
}

//Start ..
func (s *WebServer) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.Server.Addr)
	if err != nil {
		return err
	}
	if err := s.Server.Serve(ln); err != http.ErrServerClosed {
		return err
	}
	return err
}

//Stop ..
func (s *WebServer) Stop(ctx context.Context) error {
	return s.Server.Shutdown(ctx)
}

// String ..
func (s *WebServer) String() string {
	return filepath.Join(ServiceKind, s.name)
}
