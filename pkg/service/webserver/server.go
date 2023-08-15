package webserver

import (
	"context"
	"log"
	"net/http"
)

func New(addr string, mux http.Handler) *Server {
	return &Server{
		Mux:  mux,
		Addr: addr,
	}
}

type Server struct {
	Addr string
	Mux  http.Handler
}

func (s *Server) Start(ctx context.Context) error {
	server := &http.Server{
		Addr:    s.Addr,
		Handler: s.Mux,
	}
	go server.ListenAndServe()
	<-ctx.Done()
	log.Println("server stopped...")
	return server.Shutdown(ctx)
}

func (s *Server) String() string {
	return "server"
}
