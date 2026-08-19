// 示例: gRPC 服务通过 Nacos 注册并被 Higress 自动发现。
//
// 服务注册后，Nacos metadata 中自动包含 protocol=GRPC，
// Higress 据此使用 gRPC 协议转发请求。
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/service/discover/nacos"
	"github.com/rushteam/beauty/pkg/service/grpcserver"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func main() {
	nacosAddr := os.Getenv("NACOS_ADDR")
	if nacosAddr == "" {
		nacosAddr = "127.0.0.1:8848"
	}

	registry := nacos.NewRegistry(&nacos.Config{
		Addr:      []string{nacosAddr},
		Namespace: "public",
		Group:     "DEFAULT_GROUP",
	})

	app := beauty.New(
		beauty.WithRegistry(registry),
		beauty.WithGrpcServer(":9001", func(s *grpc.Server) {
			hsrv := health.NewServer()
			grpc_health_v1.RegisterHealthServer(s, hsrv)
			hsrv.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
			slog.Info("user-svc registered gRPC health service")
		},
			grpcserver.WithServiceName("user-svc"),
		),
	)

	slog.Info("user-svc starting", "nacos", nacosAddr, "listen", ":9001")
	if err := app.Start(context.Background()); err != nil {
		slog.Error("user-svc exited", "err", err)
		os.Exit(1)
	}
}
