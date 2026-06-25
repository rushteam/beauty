package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"

	"{{.ImportPath}}internal/bootstrap"
	"{{.ImportPath}}internal/infra/config"
	infralog "{{.ImportPath}}internal/infra/log"
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

	if *version {
		fmt.Printf("{{.Name}} %s (commit %s, built %s)\n", Version, Commit, BuildTime)
		return
	}

	// Ring 4：加载配置
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// Ring 4：初始化日志（带 trace 关联）
	slog.SetDefault(infralog.New(cfg))
	slog.Info("启动服务", "name", cfg.App, "version", Version)

	// 组合根：装配应用
	app := bootstrap.New(cfg)
	if err := app.Start(context.Background()); err != nil {
		slog.Error("服务启动失败", "error", err)
		log.Fatalln(err)
	}
}
