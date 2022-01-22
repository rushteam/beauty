package web

import (
	"context"
	"net"
	"net/http"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"github.com/rushteam/beauty/pkg/config"
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
		if len(addr) > 0 {
			s.Server.Addr = addr
		}
	}
}
func WithConfig(conf config.Config) Option {
	return func(s *WebServer) {
		addr := conf.GetString("addr")
		if len(addr) > 0 {
			s.Server.Addr = addr
		}
	}
}

func WithRouter(router Router) Option {
	return func(s *WebServer) {
		router(s)
	}
}

//New new a WebServer with the name
func New(name string, opts ...Option) *WebServer {
	x := gin.New()
	x.Use(recoverMiddleware())
	s := &WebServer{
		name:   name,
		Mode:   DebugMode,
		Engine: x,
		Server: &http.Server{
			Handler: x,
			Addr:    ":http",
		},
	}
	for _, opt := range opts {
		opt(s)
	}
	if len(s.Mode) > 0 {
		gin.SetMode(s.Mode)
	}
	return s
}

// func WithServer(name string, opts ...Option) beauty.AppOption {
// 	return func(app *beauty.App) {
// 		s := MustNew(name, opts...)
// 		app.AppendService(s)
// 	}
// }

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
