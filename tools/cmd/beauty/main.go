package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rushteam/beauty/tools/internal/cmd/add"
	"github.com/rushteam/beauty/tools/internal/cmd/api"
	"github.com/rushteam/beauty/tools/internal/cmd/dev"
	"github.com/rushteam/beauty/tools/internal/cmd/new"
	"github.com/urfave/cli/v3"
)

// Version ..
var Version = "0.0.3"

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
				Usage:   "🆕 创建新项目或向现有项目添加服务",
				Description: `创建新的Beauty项目或向现有项目添加服务：
   • 支持创建新项目：beauty new my-project
   • 支持向现有项目添加服务：beauty new . --grpc
   • 智能检测现有项目结构
   • 支持多种服务类型组合`,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "template",
						Aliases: []string{"t"},
						Usage:   "项目模板类型",
						Value:   "unified",
					},
					&cli.StringFlag{
						Name:    "module",
						Aliases: []string{"m"},
						Usage:   "Go 模块路径(如 github.com/org/svc)，默认使用项目名",
					},
					&cli.StringFlag{
						Name:    "path",
						Aliases: []string{"p"},
						Usage:   "项目路径",
					},
					&cli.BoolFlag{
						Name:  "with-docker",
						Usage: "包含Docker配置(Dockerfile + docker-compose)",
					},
					&cli.BoolFlag{
						Name:  "with-k8s",
						Usage: "包含Kubernetes部署清单",
					},
					&cli.BoolFlag{
						Name:  "with-ci",
						Usage: "包含CI配置(GitHub Actions + golangci-lint)",
					},
					&cli.BoolFlag{
						Name:  "dry-run",
						Usage: "仅预览将生成的文件，不写入磁盘",
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
			add.Command(),
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
					config := cmd.String("config")
					watch := cmd.Bool("watch")
					if watch {
						fmt.Printf("🚀 开发模式(热重载) config=%s\n", config)
					} else {
						fmt.Printf("🚀 开发模式运行 config=%s\n", config)
					}
					return dev.Action(ctx, config, watch)
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
					output := cmd.String("output")
					platform := cmd.String("platform")
					if output == "" {
						if wd, err := os.Getwd(); err == nil {
							output = filepath.Base(wd)
						} else {
							output = "app"
						}
					}

					c := exec.CommandContext(ctx, "go", "build", "-o", output, ".")
					c.Env = os.Environ()
					if platform != "" {
						parts := strings.SplitN(platform, "/", 2)
						if len(parts) == 2 {
							c.Env = append(c.Env, "GOOS="+parts[0], "GOARCH="+parts[1])
						}
					}
					c.Stdout = os.Stdout
					c.Stderr = os.Stderr

					fmt.Printf("🔨 构建 %s (%s)...\n", output, platform)
					if err := c.Run(); err != nil {
						return err
					}
					fmt.Printf("✅ 构建完成: %s\n", output)
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
