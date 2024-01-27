package webserver

import (
	"context"
	"log"
	"net"
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
	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:    s.Addr,
		Handler: s.Mux,
	}
	go func() {
		log.Println("web server serve", s.Addr)
		if err := server.Serve(ln); err != nil {
			if err != http.ErrServerClosed {
				log.Fatalf("web server listen failed: %s\n", err)
			}
		}
	}()
	<-ctx.Done()
	log.Println("web server stopped...")
	return server.Shutdown(ctx)
}

func (s *Server) String() string {
	return "web"
}
