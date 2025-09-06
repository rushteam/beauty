package main

import (
	"context"
	"flag"
	"log"
	"log/slog"
	"time"

	"{{.ImportPath}}internal/config"
	"{{.ImportPath}}internal/infra/conf"
	"{{.ImportPath}}internal/infra/logger"
	"{{.ImportPath}}internal/infra/registry"
	"{{.ImportPath}}internal/job"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/service/cron"
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
		log.Printf("{{.Name}} Cron v1.0.0")
		return
	}

	// 加载配置
	cfg := &config.Config{}
	if err := conf.Load(*configPath, cfg); err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 初始化日志
	slog.SetDefault(logger.New(&cfg.Log))
	slog.Info("启动定时任务服务", "name", cfg.GetAppName(), "version", "1.0.0")

	// 创建服务注册器
	var registryOption beauty.Option
	if cfg.Registry.Type == "etcd" {
		registryOption = beauty.WithRegistry(etcdv3.NewRegistry(&etcdv3.Config{
			Endpoints: cfg.Registry.Endpoints,
			Prefix:    cfg.Registry.Config["prefix"],
		}))
	}

	// 创建定时任务
	cronJobs := job.NewCronJobs(cfg)

	// 创建应用
	app := beauty.New(
		// 定时任务服务
		beauty.WithCrontab(cronJobs.GetOptions()...),
		
		// 服务注册
		registryOption,
		
		// 链路追踪
		beauty.WithTrace(),
	)

	// 启动应用
	slog.Info("定时任务服务启动中...")
	if err := app.Start(context.Background()); err != nil {
		slog.Error("定时任务服务启动失败", "error", err)
		log.Fatalln(err)
	}
}
