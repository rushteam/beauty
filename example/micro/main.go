package main

import (
	"context"
	"log"
	"net/http"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/discover"
)

func main() {
	r := http.NewServeMux()
	r.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Welcome"))
	})

	ctx := context.Background()
	app := beauty.New(
		beauty.WithRegistry(discover.EmptyRegistry{}),
		beauty.WithWebServer(":8080", r, beauty.WithServiceName("web")),
	)
	if err := app.Start(ctx); err != nil {
		log.Fatalln(err)
	}
}
