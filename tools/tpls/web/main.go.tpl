package main

import (
	"context"
	"flag"
	"log"

	"{{.ModPath}}internal/router"

	"github.com/rushteam/beauty"
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
