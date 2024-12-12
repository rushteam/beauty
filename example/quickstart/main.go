package main

import (
	"context"
	// v1 "example/example/api/v1"
	"log"

	"github.com/rushteam/beauty"
	"google.golang.org/grpc"
)

type greeterServer struct {
	// v1.UnimplementedGreeterServer
}

func main() {
	app := beauty.New()

	// 注册 gRPC 服务
	app.GRPC(":9090", func(srv *grpc.Server) {
		// v1.RegisterGreeterServer(srv, &greeterServer{})
	})

	if err := app.Start(context.Background()); err != nil {
		log.Fatal(err)
	}
}
