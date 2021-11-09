package web

import (
	"context"
	"log"
	"net"
	"net/http"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"github.com/rushteam/beauty"
)

//ServiceKind ..
const ServiceKind = "webserver"

const DebugMode = gin.DebugMode
const ReleaseMode = gin.ReleaseMode
const TestMode = gin.TestMode

type Option func(s *WebServer)
type Router func(s *WebServer)

func WithMode(mode string) Option {
	return func(s *WebServer) {
		s.Mode = mode
	}
}
func WithAddr(addr string) Option {
	return func(s *WebServer) {
		s.Server.Addr = addr
	}
}

func WithRouter(router Router) Option {
	return func(s *WebServer) {
		router(s)
	}
}

//New new a WebServer with the name
func New(name string, opts ...Option) (*WebServer, error) {
	x := gin.New()
	x.Use(recoverMiddleware())
	s := &WebServer{
		name:   name,
		Mode:   DebugMode,
		Engine: x,
		Server: &http.Server{
			Handler: x,
		},
	}
	for _, opt := range opts {
		opt(s)
	}
	if len(s.Mode) > 0 {
		gin.SetMode(s.Mode)
	}
	return s, nil
}

// New new a WebServer
func MustNew(name string, opts ...Option) *WebServer {
	s, err := New(name, opts...)
	if err != nil {
		log.Fatal(err)
	}
	return s
}
func WithServer(opts ...Option) beauty.AppOption {
	return func(app *beauty.App) {
		s := MustNew("test", opts...)
		app.AppendService(s)
	}
}

//WebServer ..
type WebServer struct {
	name string
	*gin.Engine
	*http.Server
	Mode string
}

//Start ..
func (s *WebServer) Start(ctx context.Context) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", s.Server.Addr)
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
