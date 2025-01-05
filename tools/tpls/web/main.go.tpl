package main

import (
	"context"
	"flag"
	"log"

	"{{.ImportPath}}internal/router"

	"github.com/rushteam/beauty"
)

var port string

func main() {
	flag.StringVar(&port, "port", ":8080", "")
	flag.Parse()
	app := beauty.New(
		beauty.WithWebServer(
			port,
			router.NewRoutes(),
		),
	)
	if err := app.Start(context.Background()); err != nil {
		log.Fatalln(err)
	}
}

// router.Middlewares,
