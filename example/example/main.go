package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/rushteam/beauty"
)

func main() {
	s := &srv{}
	s2 := &srv{}
	var routes = []beauty.Route{
		{
			URI: "/",
			Handler: func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte("Welcome"))
			},
		},
	}

	app := beauty.New(
		beauty.WithService(s, s2),
		beauty.WithWebServer(
			":8080",
			beauty.WithWebRoutes(routes...),
			beauty.WithWebDefaultMiddleware(),
		),
	)
	if err := app.Start(context.Background()); err != nil {
		log.Fatalln(err)
	}
}

type srv struct {
}

func (s *srv) Start(ctx context.Context) error {
	fmt.Println("..")
	return nil
}
func (s *srv) String() string {
	return "server"
}
