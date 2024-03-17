package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/discover"
	"github.com/rushteam/beauty/pkg/service/grpcclient"
)

func main() {
	r := http.NewServeMux()
	r.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Welcome"))
	})

	go func() {
		time.Sleep(2 * time.Second)
		client, err := grpcclient.New(grpcclient.WithAddr(":33000"))
		fmt.Println(client, err)
		// client.ClientConn
	}()
	ctx := context.Background()
	app := beauty.New(
		beauty.WithRegistry(discover.NewNoop()),
		beauty.WithWebServer(":8080", r, beauty.WithServiceName("web")),
	)
	if err := app.Start(ctx); err != nil {
		log.Fatalln(err)
	}
}
