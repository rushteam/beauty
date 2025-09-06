package main

import (
	"context"
	"flag"
	"log"
	"log/slog"

	"{{.ImportPath}}internal/config"
	"{{.ImportPath}}internal/infra/conf"
	"{{.ImportPath}}internal/infra/logger"
	{{if .EnableWeb}}"{{.ImportPath}}internal/endpoint/router"
	{{end}}{{if .EnableGrpc}}"{{.ImportPath}}internal/endpoint/grpc"
	"{{.ImportPath}}internal/infra/middleware"
	{{end}}{{if .EnableCron}}"{{.ImportPath}}internal/job"
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

func main() {
	flag.Parse()

	// 显示版本信息
	if *version {
		{{if and .EnableWeb .EnableGrpc .EnableCron}}log.Printf("{{.Name}} Full-Stack v1.0.0")
		{{else if and .EnableWeb .EnableGrpc}}log.Printf("{{.Name}} Web+gRPC v1.0.0")
		{{else if and .EnableWeb .EnableCron}}log.Printf("{{.Name}} Web+Cron v1.0.0")
		{{else if and .EnableGrpc .EnableCron}}log.Printf("{{.Name}} gRPC+Cron v1.0.0")
		{{else if .EnableWeb}}log.Printf("{{.Name}} Web v1.0.0")
		{{else if .EnableGrpc}}log.Printf("{{.Name}} gRPC v1.0.0")
		{{else if .EnableCron}}log.Printf("{{.Name}} Cron v1.0.0")
		{{else}}log.Printf("{{.Name}} v1.0.0")
		{{end}}
		return
	}

	// 加载配置
	cfg := &config.Config{}
	if err := conf.Load(*configPath, cfg); err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 初始化日志
	slog.SetDefault(logger.New(&cfg.Log))
	{{if and .EnableWeb .EnableGrpc .EnableCron}}slog.Info("启动全栈服务", "name", cfg.GetAppName(), "version", "1.0.0")
	{{else if and .EnableWeb .EnableGrpc}}slog.Info("启动Web+gRPC服务", "name", cfg.GetAppName(), "version", "1.0.0")
	{{else if and .EnableWeb .EnableCron}}slog.Info("启动Web+定时任务服务", "name", cfg.GetAppName(), "version", "1.0.0")
	{{else if and .EnableGrpc .EnableCron}}slog.Info("启动gRPC+定时任务服务", "name", cfg.GetAppName(), "version", "1.0.0")
	{{else if .EnableWeb}}slog.Info("启动Web服务", "name", cfg.GetAppName(), "version", "1.0.0")
	{{else if .EnableGrpc}}slog.Info("启动gRPC服务", "name", cfg.GetAppName(), "version", "1.0.0")
	{{else if .EnableCron}}slog.Info("启动定时任务服务", "name", cfg.GetAppName(), "version", "1.0.0")
	{{else}}slog.Info("启动服务", "name", cfg.GetAppName(), "version", "1.0.0")
	{{end}}

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
		append([]grpcserver.Options{
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
