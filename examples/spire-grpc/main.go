// 示例:用 contrib/spire 给 gRPC 服务做 mTLS,并从对端 SPIFFE ID 取身份。
//
// 需要本机 SPIRE Agent 可用,且当前进程已登记 workload entry。
// 默认读环境变量 SPIFFE_ENDPOINT_SOCKET;也可用 -socket 覆盖。
//
//	go run . -mode=server
//	go run . -mode=client -addr=127.0.0.1:9090
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/contrib/spire"
	"github.com/rushteam/beauty/pkg/middleware/auth"
	"github.com/rushteam/beauty/pkg/service/grpcserver"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func main() {
	mode := flag.String("mode", "server", "server|client")
	addr := flag.String("addr", "127.0.0.1:9090", "listen or dial address")
	socket := flag.String("socket", "", "Workload API socket (empty = SPIFFE_ENDPOINT_SOCKET)")
	flag.Parse()

	ctx := context.Background()
	var opts []spire.Option
	if *socket != "" {
		opts = append(opts, spire.WithAddr(*socket))
	}
	source, err := spire.Connect(ctx, opts...)
	if err != nil {
		slog.Error("spire connect failed", "err", err)
		os.Exit(1)
	}
	defer source.Close()

	id, err := source.SPIFFEID()
	if err != nil {
		slog.Error("read local svid", "err", err)
		os.Exit(1)
	}
	slog.Info("local spiffe id", "id", id.String())

	switch *mode {
	case "server":
		if err := runServer(source, *addr); err != nil {
			slog.Error("server failed", "err", err)
			os.Exit(1)
		}
	case "client":
		if err := runClient(ctx, source, *addr); err != nil {
			slog.Error("client failed", "err", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown mode %q\n", *mode)
		os.Exit(2)
	}
}

func runServer(source *spire.Source, addr string) error {
	// grpcserver 自动注册 health;handler 可为空或挂业务服务。
	app := beauty.New(
		beauty.WithComponent(source),
		beauty.WithGrpcServer(addr, nil,
			grpcserver.WithGrpcServerOptions(source.ServerCredsOption(spire.AuthorizeAny())),
			grpcserver.WithGrpcServerUnaryInterceptor(
				spire.UnaryServerInterceptor(
					spire.WithAuthzSubject(),
					spire.WithSkipPaths(
						"/grpc.health.v1.Health/Check",
						"/grpc.health.v1.Health/Watch",
					),
					spire.WithOnAuthSuccess(func(_ context.Context, peerID spiffeid.ID) {
						slog.Info("peer authenticated", "spiffe_id", peerID.String())
					}),
				),
				func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
					if u, ok := auth.GetUserFromContext(ctx); ok {
						slog.Info("handler sees user", "id", u.ID(), "method", info.FullMethod)
					}
					return handler(ctx, req)
				},
			),
		),
	)
	slog.Info("spire mTLS gRPC server listening", "addr", addr)
	return app.Start(context.Background())
}

func runClient(ctx context.Context, source *spire.Source, addr string) error {
	conn, err := grpc.NewClient(addr, source.DialCredsOption(spire.AuthorizeAny()))
	if err != nil {
		return err
	}
	defer conn.Close()

	client := grpc_health_v1.NewHealthClient(conn)
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := client.Check(cctx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		return err
	}
	slog.Info("health check ok", "status", resp.Status.String())
	return nil
}
