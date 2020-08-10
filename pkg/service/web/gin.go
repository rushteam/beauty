package web

import (
	"context"
	"net"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rushteam/beauty/pkg/config"
	"github.com/rushteam/beauty/pkg/log"
	"github.com/rushteam/beauty/pkg/registry"
)

//ServiceKind ..
const ServiceKind = "web.gin"

//Build create a web service with the name
func Build(name string) (*Web, error) {
	s := &Web{
		service: &registry.Service{
			Kind: ServiceKind,
			Name: name,
		},
		Engine: gin.New(),
		Mode:   gin.DebugMode,
		Addr:   ":http",
	}
	if conf, err := config.New(config.Env(), name); err == nil {
		s.Mode = conf.GetString(ServiceKind + ".mode")
		s.Addr = conf.GetString(ServiceKind + ".addr")
	} else {
		log.Logger.Warn("no config file...")
	}
	s.Server = &http.Server{
		Handler: s.Engine,
	}
	gin.SetMode(s.Mode)
	return s, nil
}

//Web ..
type Web struct {
	*gin.Engine
	Server  *http.Server
	listen  *net.Listener
	service *registry.Service
	Mode    string
	Addr    string
}

//Start ..
func (s *Web) Start(ctx context.Context) error {
	// log.Logger.Info("start with", ServiceKind, s)
	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return err
	}
	if err := s.Server.Serve(ln); err != http.ErrServerClosed {
		return err
	}
	return nil
}

//Stop ..
func (s *Web) Stop(ctx context.Context) error {
	return s.Server.Shutdown(ctx)
}

//Service ..
func (s *Web) Service() *registry.Service {
	return s.service
}
