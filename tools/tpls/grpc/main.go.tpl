package main

import (
	"context"
	"flag"
	"log"
	"log/slog"

	"{{.ImportPath}}internal/config"
	"{{.ImportPath}}internal/endpoint/grpc"
	"{{.ImportPath}}internal/infra/conf"
	"{{.ImportPath}}internal/infra/logger"
	"{{.ImportPath}}internal/infra/registry"
	"{{.ImportPath}}internal/infra/middleware"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/service/telemetry"
	"github.com/rushteam/beauty/pkg/service/discover/etcdv3"
	"google.golang.org/grpc"
)

var (
	configPath = flag.String("config", "config/dev/app.yaml", "配置文件路径")
	version    = flag.Bool("version", false, "显示版本信息")
)

func main() {
	flag.Parse()

	// 显示版本信息
	if *version {
		log.Printf("{{.Name}} gRPC v1.0.0")
		return
	}

	// 加载配置
	cfg := &config.Config{}
	if err := conf.Load(*configPath, cfg); err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 初始化日志
	slog.SetDefault(logger.New(&cfg.Log))
	slog.Info("启动gRPC服务", "name", cfg.GetAppName(), "version", "1.0.0")

	// 创建服务注册器
	var registryOption beauty.Option
	if cfg.Registry.Type == "etcd" {
		registryOption = beauty.WithRegistry(etcdv3.NewRegistry(&etcdv3.Config{
			Endpoints: cfg.Registry.Endpoints,
			Prefix:    cfg.Registry.Config["prefix"],
		}))
	}

	// 创建中间件
	middlewares := middleware.New(cfg)

	// 创建gRPC服务器
	grpcServer := grpc.NewServer()
	grpc.RegisterServices(grpcServer, cfg)

	// 创建应用
	app := beauty.New(
		// gRPC服务
		beauty.WithGrpcServer(
			cfg.HTTP.Addr,
			func(s *grpc.Server) {
				grpc.RegisterServices(s, cfg)
			},
			beauty.WithServiceName(cfg.App),
		),
		
		// 服务注册
		registryOption,
		
		// 链路追踪
		beauty.WithTrace(),
		
		// 指标监控
		beauty.WithMetric(telemetry.WithMetricReader(telemetry.NewPrometheusReader())),
		
		// 中间件
		middlewares.GetOptions()...,
	)

	// 启动应用
	slog.Info("gRPC服务启动中...", "addr", cfg.GetHTTPAddr())
	if err := app.Start(context.Background()); err != nil {
		slog.Error("gRPC服务启动失败", "error", err)
		log.Fatalln(err)
	}
}
