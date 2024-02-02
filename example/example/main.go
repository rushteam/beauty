package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	// "github.com/go-chi/chi"

	"github.com/rushteam/beauty"
	v1 "github.com/rushteam/beauty/example/example/api/v1"
	"github.com/rushteam/beauty/pkg/service/grpcgw"
	"google.golang.org/grpc"
)

// protoc --go_out=. --go-grpc_out=. service.proto
func main() {
	// s := &srv{}
	// s2 := &srv{}
	r := http.NewServeMux()
	r.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Welcome"))
	})
	gw := grpcgw.New()
	v1.RegisterGreeterHandlerServer(context.Background(), gw, &GreeterServer{})

	app := beauty.New(
		// beauty.WithService(s, s2),
		beauty.WithWebServer(
			":8080",
			gw,
		),
		beauty.WithWebServer(
			":http",
			r,
		),
		beauty.WithGrpcServer(
			":58080",
			func(s *grpc.Server) {
				v1.RegisterGreeterServer(s, &GreeterServer{})
			},
		),
	)
	if err := app.Start(context.Background()); err != nil {
		log.Fatalln(err)
	}
}

type GreeterServer struct {
	v1.UnimplementedGreeterServer
}

func (GreeterServer) SayHello(context.Context, *v1.HelloRequest) (*v1.HelloReply, error) {
	fmt.Println("hello world")
	return &v1.HelloReply{}, nil
}

// type srv struct {
// }

// func (s *srv) Start(ctx context.Context) error {
// 	return nil
// }
// func (s *srv) String() string {
// 	return "empty server"
// }
