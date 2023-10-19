package main

import (
	"context"
	"flag"
	"log"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/tools/test/internal/router"
)

var port string

func main() {
	flag.StringVar(&port, "port", ":8080", "")
	flag.Parse()
	app := beauty.New(
		beauty.WithWebServer(
			port,
			router.Routes,
			beauty.WithWebDefaultMiddleware(),
		),
	)
	if err := app.Start(context.Background()); err != nil {
		log.Fatalln(err)
	}
}
