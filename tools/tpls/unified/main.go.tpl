package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"

	"{{.ImportPath}}internal/config"
	"{{.ImportPath}}internal/infra/conf"
	"{{.ImportPath}}internal/infra/logger"
	{{if .EnableWeb}}"{{.ImportPath}}internal/endpoint/router"
	{{end}}{{if .EnableGrpc}}"{{.ImportPath}}internal/endpoint/grpc"
	"{{.ImportPath}}internal/infra/middleware"
	{{end}}	{{if .EnableCron}}"{{.ImportPath}}internal/endpoint/job"
	{{end}}
	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/service/telemetry"
	"github.com/rushteam/beauty/pkg/service/discover/etcdv3"
	{{if .EnableGrpc}}"github.com/rushteam/beauty/pkg/service/grpcserver"
	grpcpkg "google.golang.org/grpc"
	{{end}}
)

var (
	configPath = flag.String("config", "config/dev/app.yaml", "配置文件路径")
	version    = flag.Bool("version", false, "显示版本信息")
)

// 构建信息（由 Makefile/Dockerfile 通过 -ldflags -X 注入）
var (
	Version   = "dev"
	Commit    = "none"
	BuildTime = "unknown"
)

func main() {
	flag.Parse()

	// 显示版本信息
	if *version {
		fmt.Printf("{{.Name}} %s (commit %s, built %s)\n", Version, Commit, BuildTime)
		return
	}

	// 加载配置
	cfg := &config.Config{}
	if err := conf.Load(*configPath, cfg); err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 初始化日志
	slog.SetDefault(logger.New(&cfg.Log))
	slog.Info("启动服务", "name", cfg.GetAppName(), "version", Version)

	// 创建服务注册器
	var registryOption beauty.Option
	if cfg.Registry.Type == "etcd" {
		registryOption = beauty.WithRegistry(etcdv3.NewRegistry(&etcdv3.Config{
			Endpoints: cfg.Registry.Endpoints,
			Prefix:    cfg.Registry.Config["prefix"],
			TTL:       10,
		}))
	}

	// 创建应用选项
	var options []beauty.Option

	{{if .EnableWeb}}
	// HTTP 服务
	options = append(options, router.New(cfg))
	{{end}}

	{{if .EnableGrpc}}
	// gRPC 服务
	middlewares := middleware.New(cfg)
	options = append(options, beauty.WithService(grpcserver.New(
		cfg.GRPC.Addr,
		func(s *grpcpkg.Server) {
			grpc.RegisterServices(s, cfg)
		},
		append([]grpcserver.Option{
			grpcserver.WithServiceName(cfg.App),
		}, middlewares.GetGrpcServerOptions()...)...,
	)))
	{{end}}

	{{if .EnableCron}}
	// 定时任务服务
	cronJobs := job.NewCronJobs(cfg)
	options = append(options, beauty.WithCrontab(cronJobs.GetOptions()...))
	{{end}}

	// 服务注册
	if registryOption != nil {
		options = append(options, registryOption)
	}

	// 链路追踪
	options = append(options, beauty.WithTrace())

	// 指标监控
	options = append(options, beauty.WithMetric(telemetry.WithMetricStdoutReader()))

	// 创建应用
	app := beauty.New(options...)

	// 启动应用
	{{if .EnableWeb}}slog.Info("HTTP服务启动中...", "addr", cfg.GetHTTPAddr())
	{{end}}{{if .EnableGrpc}}slog.Info("gRPC服务启动中...", "addr", cfg.GetGRPCAddr())
	{{end}}{{if .EnableCron}}slog.Info("定时任务服务启动中...")
	{{end}}
	if err := app.Start(context.Background()); err != nil {
		slog.Error("服务启动失败", "error", err)
		log.Fatalln(err)
	}
}
