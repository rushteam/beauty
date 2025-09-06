package main

import (
	"fmt"
	"log"
	"os"

	"github.com/rushteam/beauty/tools/internal/cmd/api"
	"github.com/rushteam/beauty/tools/internal/cmd/new"
	"github.com/urfave/cli/v2"
)

// Version ..
var Version = "0.0.1"

func main() {
	app := &cli.App{
		Name:    "beauty",
		Usage:   "🚀 Beauty Framework - 微服务开发工具链",
		Version: Version,
		Description: `Beauty是一个Go微服务框架，提供完整的开发工具链：
   • 快速创建项目模板
   • 解析API定义（支持protobuf和传统格式）
   • 自动生成代码和文档
   • 集成服务发现、监控、中间件等`,
		Authors: []*cli.Author{
			{Name: "Beauty Team", Email: "team@beauty.dev"},
		},
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
				Description: `快速创建新的Beauty项目，支持多种模板：
   • web-service    - HTTP微服务
   • grpc-service   - gRPC微服务  
   • cron-service   - 定时任务服务
   • full-stack     - 完整微服务栈`,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "template",
						Aliases: []string{"t"},
						Usage:   "项目模板类型",
						Value:   "web-service",
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
				Name:    "dev",
				Aliases: []string{"d", "serve"},
				Usage:   "🔧 开发模式",
				Description: `启动开发模式，提供：
   • 文件监控和自动重载
   • 实时API文档预览
   • 集成测试运行
   • 性能监控`,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "port",
						Usage: "开发服务器端口",
						Value: "8080",
					},
					&cli.BoolFlag{
						Name:  "watch",
						Usage: "监控文件变化",
					},
					&cli.BoolFlag{
						Name:  "docs",
						Usage: "启动文档服务器",
					},
				},
				Action: func(c *cli.Context) error {
					fmt.Println("🔧 开发模式功能开发中...")
					return nil
				},
			},
			{
				Name:    "test",
				Aliases: []string{"t"},
				Usage:   "🧪 运行测试",
				Description: `运行项目测试：
   • 单元测试
   • 集成测试
   • 性能测试
   • 覆盖率报告`,
				Action: func(c *cli.Context) error {
					fmt.Println("🧪 测试功能开发中...")
					return nil
				},
			},
		},
		Before: func(c *cli.Context) error {
			if c.Bool("verbose") {
				fmt.Println("🔍 详细模式已启用")
			}
			return nil
		},
		After: func(c *cli.Context) error {
			if c.Bool("verbose") {
				fmt.Println("✅ 命令执行完成")
			}
			return nil
		},
		OnUsageError: func(c *cli.Context, err error, isSubcommand bool) error {
			fmt.Printf("❌ 使用错误: %v\n\n", err)
			cli.ShowCommandHelp(c, c.Command.Name)
			return nil
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatalf("❌ 执行失败: %v", err)
	}
}
