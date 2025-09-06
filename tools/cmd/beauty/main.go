package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/rushteam/beauty/tools/internal/cmd/api"
	"github.com/rushteam/beauty/tools/internal/cmd/new"
	"github.com/urfave/cli/v3"
)

// Version ..
var Version = "0.0.1"

func main() {
	app := &cli.Command{
		Name:    "beauty",
		Usage:   "🚀 Beauty Framework - 微服务开发工具链",
		Version: Version,
		Description: `Beauty是一个Go微服务框架，提供完整的开发工具链：
   • 快速创建项目模板
   • 解析API定义（支持protobuf和传统格式）
   • 自动生成代码和文档
   • 集成服务发现、监控、中间件等`,
		// Authors: []*cli.Author{
		// 	{Name: "Beauty Team", Email: "team@beauty.dev"},
		// },
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "verbose",
				Usage: "显示详细输出",
			},
			&cli.BoolFlag{
				Name:    "interactive",
				Aliases: []string{"i"},
				Usage:   "启用交互模式",
			},
		},
		Commands: []*cli.Command{
			{
				Name:    "new",
				Aliases: []string{"n", "create"},
				Usage:   "🆕 创建新的Beauty项目",
				Description: `快速创建新的Beauty项目，支持多种服务类型组合：
   • web-service    - HTTP微服务
   • grpc-service   - gRPC微服务  
   • cron-service   - 定时任务服务
   • unified        - 交互式选择服务类型（推荐）`,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "template",
						Aliases: []string{"t"},
						Usage:   "项目模板类型",
						Value:   "unified",
					},
					&cli.StringFlag{
						Name:    "path",
						Aliases: []string{"p"},
						Usage:   "项目路径",
					},
					&cli.BoolFlag{
						Name:  "with-docker",
						Usage: "包含Docker配置",
					},
					&cli.BoolFlag{
						Name:  "with-k8s",
						Usage: "包含Kubernetes配置",
					},
					&cli.BoolFlag{
						Name:  "web",
						Usage: "启用HTTP服务",
					},
					&cli.BoolFlag{
						Name:  "grpc",
						Usage: "启用gRPC服务",
					},
					&cli.BoolFlag{
						Name:  "cron",
						Usage: "启用定时任务服务",
					},
				},
				Action: new.Action,
			},
			{
				Name:    "api",
				Aliases: []string{"a", "parse"},
				Usage:   "📡 解析API定义文件",
				Description: `解析API定义文件并生成代码：
   • 支持protobuf (.proto) 格式
   • 支持传统api.spec格式
   • 自动生成gRPC和HTTP代码
   • 生成OpenAPI文档`,
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:    "generate",
						Aliases: []string{"g"},
						Usage:   "生成代码(非交互模式)",
					},
					&cli.StringFlag{
						Name:    "out",
						Aliases: []string{"o"},
						Value:   "gen/go",
						Usage:   "代码输出目录",
					},
					&cli.BoolFlag{
						Name:  "openapi",
						Usage: "同时生成OpenAPI文档",
					},
					&cli.BoolFlag{
						Name:  "json",
						Usage: "输出JSON格式",
					},
					&cli.BoolFlag{
						Name:  "offline",
						Usage: "离线模式（不下载依赖）",
					},
				},
				Action: api.Action,
			},
			{
				Name:        "dev",
				Aliases:     []string{"d", "run"},
				Usage:       "🚀 开发模式运行服务",
				Description: `在开发模式下运行服务，支持热重载和调试`,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "config",
						Aliases: []string{"c"},
						Usage:   "配置文件路径",
						Value:   "config/dev/app.yaml",
					},
					&cli.BoolFlag{
						Name:  "watch",
						Usage: "监听文件变化",
					},
					&cli.BoolFlag{
						Name:  "debug",
						Usage: "启用调试模式",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					fmt.Println("🚀 开发模式功能开发中...")
					return nil
				},
			},
			{
				Name:        "build",
				Aliases:     []string{"b"},
				Usage:       "🔨 构建项目",
				Description: `构建项目为可执行文件`,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "output",
						Aliases: []string{"o"},
						Usage:   "输出文件名",
					},
					&cli.StringFlag{
						Name:    "platform",
						Aliases: []string{"p"},
						Usage:   "目标平台",
						Value:   "linux/amd64",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					fmt.Println("🔨 构建功能开发中...")
					return nil
				},
			},
		},
		// Before: func(ctx context.Context, cmd *cli.Command) error {
		// 	// 全局前置处理
		// 	return nil
		// },
		// After: func(ctx context.Context, cmd *cli.Command) error {
		// 	// 全局后置处理
		// 	return nil
		// },
		OnUsageError: func(ctx context.Context, cmd *cli.Command, err error, isSubcommand bool) error {
			fmt.Fprintf(os.Stderr, "❌ 使用错误: %v\n", err)
			cli.ShowCommandHelp(ctx, cmd, cmd.Name)
			return nil
		},
	}

	err := app.Run(context.Background(), os.Args)
	if err != nil {
		log.Fatal(err)
	}
}
