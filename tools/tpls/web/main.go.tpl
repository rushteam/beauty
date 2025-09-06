package main

import (
	"context"
	"flag"
	"log"
	"log/slog"

	"{{.ImportPath}}internal/config"
	"{{.ImportPath}}internal/endpoint/router"
	"{{.ImportPath}}internal/infra/conf"
	"{{.ImportPath}}internal/infra/logger"
	"{{.ImportPath}}internal/infra/registry"
	"{{.ImportPath}}internal/infra/middleware"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/service/telemetry"
	"github.com/rushteam/beauty/pkg/service/discover/etcdv3"
)

var (
	configPath = flag.String("config", "config/dev/app.yaml", "配置文件路径")
	version    = flag.Bool("version", false, "显示版本信息")
)

func main() {
	flag.Parse()

	// 显示版本信息
	if *version {
		log.Printf("{{.Name}} v1.0.0")
		return
	}

	// 加载配置
	cfg := &config.Config{}
	if err := conf.Load(*configPath, cfg); err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 初始化日志
	slog.SetDefault(logger.New(&cfg.Log))
	slog.Info("启动服务", "name", cfg.GetAppName(), "version", "1.0.0")

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

	// 创建应用
	app := beauty.New(
		// 基础服务
		router.New(cfg),
		
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
	slog.Info("服务启动中...", "addr", cfg.GetHTTPAddr())
	if err := app.Start(context.Background()); err != nil {
		slog.Error("服务启动失败", "error", err)
		log.Fatalln(err)
	}
}
