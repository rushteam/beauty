package main

import (
	"context"
	"log"

	"github.com/rushteam/beauty"
	"gitlab.meitu.com/golang/beauty/example/grpc/service/helloworld"
	"gitlab.meitu.com/golang/beauty/pkg/service/grpc"
)

func main() {
	app := beauty.New()
	svc := service()
	if err := app.Run(svc); err != nil {
		log.Fatalln(err)
	}
}
func service() beauty.Service {
	srv, err := grpc.Build("my_grpc")
	if err != nil {
		log.Fatalln(err)
	}
	helloworld.RegisterGreeterServer(srv.Server, &helloWorldHandler{})
	return srv
}

type helloWorldHandler struct {
	helloworld.GreeterServer
}

//SayHello ..
func (h *helloWorldHandler) SayHello(ctx context.Context, in *helloworld.HelloRequest) (*helloworld.HelloReply, error) {
	log.Printf("Received: %v", in.GetName())
	return &helloworld.HelloReply{Message: "Hello " + in.GetName()}, nil
}
