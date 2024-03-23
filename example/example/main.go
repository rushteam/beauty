package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	// "github.com/go-chi/chi"

	"github.com/rushteam/beauty"
	v1 "github.com/rushteam/beauty/example/example/api/v1"
	"github.com/rushteam/beauty/pkg/discover/etcdv3"
	_ "github.com/rushteam/beauty/pkg/discover/etcdv3"
	"github.com/rushteam/beauty/pkg/service/grpcclient"
	"github.com/rushteam/beauty/pkg/service/grpcgw"
	"github.com/rushteam/beauty/pkg/tracing"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	// "go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
)

// protoc --go_out=. --go-grpc_out=. service.proto
func main() {
	// s := &srv{}
	// s2 := &srv{}
	r := http.NewServeMux()
	r.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// span := trace.SpanFromContext(r.Context())
		_, span := tracing.SpanFromContext(r.Context(), "http")
		defer span.End()
		span.SetAttributes(attribute.String("url", r.URL.String()))
		span.AddEvent("request")
		w.Write([]byte("Welcome"))
	})
	r.HandleFunc("/123", func(w http.ResponseWriter, r *http.Request) {
		_, span := otel.Tracer("http").Start(context.Background(), "request")
		// span := trace.SpanFromContext(context.Background())
		defer span.End()
		span.SetAttributes(attribute.String("url", r.URL.String()))
		time.Sleep(10 * time.Second)
		w.Write([]byte("hi"))
	})

	go func() {
		time.Sleep(time.Second * 5)
		client, err := grpcclient.New(
			// grpcclient.WithDiscover("etcd:///127.0.0.1"),
			grpcclient.WithAddr("etcd://127.0.0.1:2379,127.0.0.2:2379/beauty/helloworld.rpc"),
		)
		if err != nil {
			fmt.Println("client>", err)
			return
		}
		c := v1.NewGreeterClient(client)
		resp, err := c.SayHello(context.Background(), &v1.HelloRequest{})
		if err != nil {
			fmt.Println("client>", err)
			return
		}
		fmt.Println("client>", resp, err)
		time.Sleep(time.Second * 10)
		client.Close()
	}()

	gw := grpcgw.New()
	v1.RegisterGreeterHandlerServer(context.Background(), gw, &GreeterServer{})

	app := beauty.New(
		// beauty.WithService(s, s2),
		// beauty.WithRegistry(discover.NewNoop()),
		beauty.WithTrace(),
		beauty.WithRegistry(etcdv3.NewEtcdRegistry(etcdv3.EtcdConfig{
			Endpoints: []string{
				"127.0.0.1:2379",
			},
			Namespace: "/beauty",
		})),
		beauty.WithWebServer(
			":8080",
			gw,
			beauty.WithServiceName("helloworld.gw"),
		),
		beauty.WithWebServer(
			":http",
			r,
			beauty.WithServiceName("helloworld.web"),
		),
		beauty.WithGrpcServer(
			":58080",
			func(s *grpc.Server) {
				v1.RegisterGreeterServer(s, &GreeterServer{})
			},
			beauty.WithServiceName("helloworld.rpc"),
			beauty.WithServiceMeta("version", "v1.0"),
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
	return &v1.HelloReply{
		Message: "hello world",
	}, nil
}

// type srv struct {
// }

// func (s *srv) Start(ctx context.Context) error {
// 	return nil
// }
// func (s *srv) String() string {
// 	return "empty server"
// }
