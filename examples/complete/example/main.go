package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	// "github.com/go-chi/chi"
	// "github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rushteam/beauty"
	v1 "github.com/rushteam/beauty/example/example/api/v1"
	"github.com/rushteam/beauty/pkg/client/grpc"

	"github.com/rushteam/beauty/pkg/discover/etcdv3"
	"github.com/rushteam/beauty/pkg/discover/nacos"

	// "github.com/rushteam/beauty/pkg/discover/nacos"
	"github.com/rushteam/beauty/pkg/service/grpcgw"
	"github.com/rushteam/beauty/pkg/service/grpcserver"
	"github.com/rushteam/beauty/pkg/service/webserver"
	"github.com/rushteam/beauty/pkg/service/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/prometheus"

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
	r.HandleFunc("/trace", func(w http.ResponseWriter, r *http.Request) {
		_, span := otel.Tracer("http").Start(context.Background(), "request")
		// span := trace.SpanFromContext(context.Background())
		defer span.End()
		span.SetAttributes(attribute.String("url", r.URL.String()))
		w.Write([]byte("trace"))
	})
	r.HandleFunc("/meter", func(w http.ResponseWriter, r *http.Request) {
		m, _ := otel.Meter("http").Int64Counter("request")
		m.Add(context.Background(), 100)
		w.Write([]byte("meter"))
	})
	// r.Handle("/metrics", promhttp.Handler())

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// time.Sleep(time.Second * 3)
		client, err := grpcclient.New(
			context.Background(),
			grpcclient.WithDefault(),
			// grpcclient.WithBlock(),
			// grpcclient.WithDiscover("etcd:///127.0.0.1"),
			grpcclient.WithBalancingPolicy("p2c_ewma"),
			// grpcclient.WithAddr("etcd://127.0.0.1:2379,127.0.0.2:2379/helloworld.rpc"),
			grpcclient.WithAddr("nacos://127.0.0.1:8848/helloworld.rpc?app_name=test"),
		)
		if err != nil {
			fmt.Println("client>error1", err)
			return
		}
		defer client.Close()
		t := time.NewTicker(time.Second * 2)
		for {
			select {
			case <-ctx.Done():
				fmt.Println("client>done")
				return
			case <-t.C:
				fmt.Println("client>call")
				func() {
					ctxTimeout, cancel := context.WithTimeout(context.Background(), time.Second)
					defer cancel()
					c := v1.NewGreeterClient(client)
					resp, err := c.SayHello(ctxTimeout, &v1.HelloRequest{})
					if err != nil {
						fmt.Println("client>call>error", err)
						return
					}
					fmt.Println("client>", resp, err)
				}()
			}
		}
	}()

	gw := grpcgw.New()
	v1.RegisterGreeterHandlerServer(context.Background(), gw, &GreeterServer{})

	metricExprter, err := prometheus.New()
	if err != nil {
		panic(err)
	}

	app := beauty.New(
		// beauty.WithService(s, s2),
		// beauty.WithRegistry(discover.NewNoop()),
		beauty.WithTrace(),
		beauty.WithMetric(tracing.WithMetricReader(metricExprter)),
		beauty.WithRegistry(etcdv3.NewRegistry(&etcdv3.Config{
			Endpoints: []string{
				"127.0.0.1:2379",
			},
			Prefix: "/beauty",
		})),
		beauty.WithRegistry(nacos.NewRegistry(&nacos.Config{
			Addr:      []string{"127.0.0.1:8848"},
			Cluster:   "",
			Namespace: "",
			Group:     "",
			Weight:    100,
		})),
		beauty.WithService(webserver.New(
			":8080",
			gw,
			webserver.WithServiceName("helloworld.gw"),
		)),
		beauty.WithService(webserver.New(
			":http",
			r,
			webserver.WithServiceName("helloworld.web"),
		)),
		beauty.WithService(grpcserver.New(
			":58080",
			func(s *grpc.Server) {
				v1.RegisterGreeterServer(s, &GreeterServer{})
			},
			grpcserver.WithServiceName("helloworld.rpc"),
			grpcserver.WithMetadata(map[string]string{"version": "v1.0"}),
		)),
		beauty.WithService(grpcserver.New(
			":58090",
			func(s *grpc.Server) {
				v1.RegisterGreeterServer(s, &GreeterServer{})
			},
			grpcserver.WithServiceName("helloworld.rpc"),
			grpcserver.WithMetadata(map[string]string{"version": "v2.0"}),
		)),
	)
	app.Hook(beauty.EventAfterRun, func(app *beauty.App) {
		cancel()
	})
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
