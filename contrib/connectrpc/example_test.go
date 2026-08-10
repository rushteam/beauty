package connectrpc_test

import (
	"fmt"
	"net/http"

	connectserver "github.com/rushteam/beauty/contrib/connectrpc"
)

func ExampleNew() {
	// 创建 Connect 服务（默认启用 H2C 和健康检查）
	srv := connectserver.New(":8080",
		connectserver.WithServiceName("my-service"),
		connectserver.WithVersion("v1.0.0"),
	)

	// 注册 Connect handler（实际使用中由 protoc-gen-connect-go 生成）:
	//   srv.Handle(pingv1connect.NewPingServiceHandler(&PingServer{}))
	//
	// 这里用普通 handler 演示：
	srv.Handle("/acme.ping.v1.PingService/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"number": 42}`)
	}))

	// 也可以混合注册普通 HTTP 路由
	srv.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	})

	// 纳入 beauty 生命周期：
	//   app := beauty.New(beauty.WithService(srv))
	//   app.Start(ctx)

	fmt.Println(srv.Kind())
	// Output: connect
}

func ExampleNew_withServiceDiscovery() {
	// 搭配服务发现（每个 protobuf 服务独立注册到注册中心）
	//
	//   etcdRegistry := etcdv3.NewRegistry(&etcdv3.Config{...})
	//   srv := connectserver.New(":8080",
	//       connectserver.WithAutoServiceDiscovery(
	//           []discover.Registry{etcdRegistry},
	//       ),
	//       connectserver.WithRegionInfo("us-west-1", "us-west-1a", "campus-1"),
	//   )
	//   srv.Handle(pingv1connect.NewPingServiceHandler(&PingServer{}))
	//   app := beauty.New(beauty.WithService(srv))

	srv := connectserver.New(":0")
	fmt.Println(srv.Kind())
	// Output: connect
}

func ExampleNewTransport() {
	// 客户端搭配服务发现：
	//
	//   discovery := etcdv3.NewRegistry(&etcdv3.Config{...})
	//   rt := connectserver.NewTransport(discovery, "acme.ping.v1.PingService")
	//   defer rt.Close()
	//
	//   httpClient := &http.Client{Transport: rt}
	//   client := pingv1connect.NewPingServiceClient(
	//       httpClient,
	//       "http://acme.ping.v1.PingService/",
	//   )
	//   res, err := client.Ping(ctx, &pingv1.PingRequest{Number: 42})

	fmt.Println("transport ready")
	// Output: transport ready
}
