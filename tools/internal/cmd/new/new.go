package new

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/gobuffalo/here"
	"github.com/rushteam/beauty/tools/internal/entity"
	"github.com/rushteam/beauty/tools/internal/pkg"
	"github.com/rushteam/beauty/tools/tpls"
	"github.com/urfave/cli/v3"
)

// Action 创建新项目的命令处理函数
func Action(ctx context.Context, c *cli.Command) error {
	args := c.Args()
	if args.Len() == 0 {
		return cli.Exit(fmt.Errorf("❌ 缺少项目名称\n\n使用示例:\n  beauty new my-project\n  beauty new my-project --template grpc-service"), 1)
	}

	// 获取命令行参数
	projectName := args.Get(0)
	template := c.String("template")
	projectPath := c.String("path")
	withDocker := c.Bool("with-docker")
	withK8s := c.Bool("with-k8s")
	verbose := c.Bool("verbose")

	// 服务类型选择
	enableWeb := c.Bool("web")
	enableGrpc := c.Bool("grpc")
	enableCron := c.Bool("cron")

	// 调试信息（仅在verbose模式下显示）
	if verbose {
		fmt.Printf("🔍 原始参数: %v\n", c.Args().Slice())
		fmt.Printf("🔍 所有标志: %v\n", c.FlagNames())
		fmt.Printf("🔍 模板标志值: %s\n", template)
	}

	// 处理服务类型选择
	if template == "unified" {
		// 交互式选择服务类型
		if !enableWeb && !enableGrpc && !enableCron {
			// 如果没有通过命令行指定，则进行交互式选择
			web, grpc, cron, err := interactiveServiceSelection()
			if err != nil {
				return fmt.Errorf("❌ 交互式选择失败: %w", err)
			}
			enableWeb = web
			enableGrpc = grpc
			enableCron = cron
		}
	} else {
		// 根据模板类型设置服务类型
		switch template {
		case "web-service":
			enableWeb = true
		case "grpc-service":
			enableGrpc = true
		case "cron-service":
			enableCron = true
		}
	}

	// 设置项目配置
	entity.Config.Name = projectName
	entity.Config.Module = projectName // 设置模块名
	entity.Config.Template = template
	entity.Config.WithDocker = withDocker
	entity.Config.WithK8s = withK8s
	entity.Config.EnableWeb = enableWeb
	entity.Config.EnableGrpc = enableGrpc
	entity.Config.EnableCron = enableCron

	if verbose {
		fmt.Printf("🔍 命令行模板类型: %s\n", template)
		fmt.Printf("🔍 设置后模板类型: %s\n", entity.Config.Template)
	}

	// 设置项目路径
	if projectPath == "" {
		pwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("❌ 获取当前目录失败: %w", err)
		}
		entity.Config.Path = filepath.Join(pwd, projectName)
	} else {
		path, err := filepath.Abs(projectPath)
		if err != nil {
			return fmt.Errorf("❌ 获取绝对路径失败: %w", err)
		}
		entity.Config.Path = path
	}

	if verbose {
		fmt.Printf("🔍 项目名称: %s\n", projectName)
		fmt.Printf("🔍 项目路径: %s\n", entity.Config.Path)
		fmt.Printf("🔍 模板类型: %s\n", template)
		fmt.Printf("🔍 包含Docker: %t\n", withDocker)
		fmt.Printf("🔍 包含K8s: %t\n", withK8s)
	}

	// 检查项目目录是否已存在
	if _, err := os.Stat(entity.Config.Path); !os.IsNotExist(err) {
		return cli.Exit(fmt.Errorf("❌ 项目目录已存在: %s\n\n💡 提示: 请选择其他名称或删除现有目录", entity.Config.Path), 1)
	}

	// 显示开始信息
	fmt.Println("🚀 开始创建Beauty项目...")
	startTime := time.Now()

	// 创建项目
	if err := createProject(entity.Config, verbose); err != nil {
		return fmt.Errorf("❌ 创建项目失败: %w", err)
	}

	// 显示完成信息
	duration := time.Since(startTime)
	fmt.Printf("\n✅ 项目创建完成! 耗时: %v\n", duration.Round(time.Millisecond))

	// 显示后续步骤
	fmt.Println("\n📋 后续步骤:")
	fmt.Printf("  cd %s\n", projectName)
	fmt.Println("  go mod tidy")
	fmt.Println("  go run main.go")

	if withDocker {
		fmt.Println("  docker build -t " + projectName + " .")
		fmt.Println("  docker run -p 8080:8080 " + projectName)
	}

	return nil
}

// createProject 创建新项目
func createProject(conf *entity.Project, verbose bool) error {
	// 创建项目目录
	if err := pkg.MkdirAll(conf.Path); err != nil {
		return fmt.Errorf("创建项目目录失败: %w", err)
	}

	// 设置模块信息
	conf.Module = conf.Name // 使用项目名称作为模块名
	conf.ImportPath = conf.Module + "/"

	// 获取模块信息（用于其他用途）
	if hi, err := here.Dir(conf.Path); err == nil {
		conf.Info = hi
	}

	if verbose {
		fmt.Printf("📁 创建项目目录: %s\n", conf.Path)
		fmt.Printf("📦 模块名称: %s\n", conf.Module)
	}

	// 根据模板类型选择不同的处理方式
	switch conf.Template {
	case "grpc-service":
		return createGrpcService(conf, verbose)
	case "cron-service":
		return createCronService(conf, verbose)
	case "unified":
		return createUnifiedService(conf, verbose)
	default: // web-service
		return createWebService(conf, verbose)
	}
}

// createWebService 创建HTTP微服务
func createWebService(conf *entity.Project, verbose bool) error {
	fmt.Println("🌐 创建HTTP微服务...")
	return buildProject(conf, verbose)
}

// createGrpcService 创建gRPC微服务
func createGrpcService(conf *entity.Project, verbose bool) error {
	fmt.Println("🔌 创建gRPC微服务...")
	return buildProject(conf, verbose)
}

// createCronService 创建定时任务服务
func createCronService(conf *entity.Project, verbose bool) error {
	fmt.Println("⏰ 创建定时任务服务...")
	return buildProject(conf, verbose)
}

// createUnifiedService 创建统一微服务
func createUnifiedService(conf *entity.Project, verbose bool) error {
	// 根据启用的服务类型显示不同的消息
	var services []string
	if conf.EnableWeb {
		services = append(services, "HTTP")
	}
	if conf.EnableGrpc {
		services = append(services, "gRPC")
	}
	if conf.EnableCron {
		services = append(services, "Cron")
	}

	serviceStr := strings.Join(services, "+")
	fmt.Printf("🚀 创建微服务 (%s)...\n", serviceStr)
	return buildProject(conf, verbose)
}

func hasExists(path string) error {
	dirs, err := os.ReadDir(".")
	if err != nil {
		return err
	}
	for _, dir := range dirs {
		if dir.Name() == path && dir.IsDir() {
			return errors.New("directory already exists")
		}
	}
	return nil
}

// buildProject 构建项目文件
func buildProject(conf *entity.Project, verbose bool) error {
	// 使用模板类型（仅在verbose模式下显示）
	if verbose {
		fmt.Printf("🔍 使用模板类型: %s\n", conf.Template)
	}
	tpl := tpls.GetTemplateRoot(conf.Template)

	return fs.WalkDir(tpl, ".", func(path string, info os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// 跳过不需要的文件
		if shouldSkipFile(path, conf) {
			return nil
		}

		if info.IsDir() {
			dirPath := filepath.Join(conf.Path, path)
			if err := pkg.MkdirAll(dirPath); err != nil {
				return err
			}
			if verbose {
				fmt.Printf("📁 创建目录: %s\n", dirPath)
			}
			return nil
		}

		// 读取模板文件
		src, err := tpl.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()

		data, err := io.ReadAll(src)
		if err != nil {
			return err
		}

		// 处理文件名
		filename := strings.TrimSuffix(path, ".tpl")
		outputPath := filepath.Join(conf.Path, filename)

		// 创建目标文件
		dst, err := pkg.Create(outputPath)
		if err != nil {
			return err
		}
		defer dst.Close()

		// 解析并执行模板
		tmpl, err := template.New(info.Name()).Parse(string(data))
		if err != nil {
			return err
		}

		if err := tmpl.Execute(dst, conf); err != nil {
			return err
		}

		if verbose {
			fmt.Printf("📄 创建文件: %s\n", outputPath)
		}

		return nil
	})
}

// shouldSkipFile 判断是否应该跳过某个文件
func shouldSkipFile(path string, conf *entity.Project) bool {
	// 对于统一模板，根据启用的服务类型决定是否跳过文件
	if conf.Template == "unified" {
		// 如果未启用 Web 服务，跳过 HTTP 相关文件
		if !conf.EnableWeb && (strings.Contains(path, "http") || strings.Contains(path, "web")) {
			return true
		}
		// 如果未启用 gRPC 服务，跳过 gRPC 相关文件
		if !conf.EnableGrpc && strings.Contains(path, "grpc") {
			return true
		}
		// 如果未启用 Cron 服务，跳过 Cron 相关文件
		if !conf.EnableCron && strings.Contains(path, "cron") {
			return true
		}
		return false
	}

	// 根据模板类型跳过不需要的文件
	switch conf.Template {
	case "grpc-service":
		// 跳过HTTP相关的模板文件
		return strings.Contains(path, "http") || strings.Contains(path, "web")
	case "cron-service":
		// 跳过HTTP和gRPC相关的模板文件
		return strings.Contains(path, "http") || strings.Contains(path, "grpc") || strings.Contains(path, "web")
	case "web-service":
		// 跳过gRPC相关的模板文件
		return strings.Contains(path, "grpc")
	}
	return false
}

// interactiveServiceSelection 交互式服务类型选择
func interactiveServiceSelection() (web, grpc, cron bool, err error) {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("\n🎯 请选择要启用的服务类型:")
	fmt.Println("   1. HTTP 服务 (REST API)")
	fmt.Println("   2. gRPC 服务 (高性能 RPC)")
	fmt.Println("   3. 定时任务服务 (Cron Jobs)")
	fmt.Println("   4. 全栈服务 (HTTP + gRPC + Cron)")
	fmt.Println("   5. 自定义组合")
	fmt.Print("\n请输入选项 (多个选项用逗号分隔，如: 1,2,3): ")

	input, err := reader.ReadString('\n')
	if err != nil {
		return false, false, false, err
	}

	input = strings.TrimSpace(input)
	options := strings.Split(input, ",")

	for _, opt := range options {
		opt = strings.TrimSpace(opt)
		switch opt {
		case "1":
			web = true
		case "2":
			grpc = true
		case "3":
			cron = true
		case "4":
			web = true
			grpc = true
			cron = true
		case "5":
			// 自定义组合
			return customServiceSelection()
		default:
			fmt.Printf("⚠️  无效选项: %s，已忽略\n", opt)
		}
	}

	// 至少选择一个服务
	if !web && !grpc && !cron {
		fmt.Println("❌ 至少需要选择一个服务类型")
		return interactiveServiceSelection()
	}

	// 显示选择结果
	fmt.Printf("\n✅ 已选择服务类型:")
	if web {
		fmt.Print(" HTTP")
	}
	if grpc {
		fmt.Print(" gRPC")
	}
	if cron {
		fmt.Print(" Cron")
	}
	fmt.Println()

	return web, grpc, cron, nil
}

// customServiceSelection 自定义服务组合选择
func customServiceSelection() (web, grpc, cron bool, err error) {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("\n🔧 自定义服务组合:")

	// HTTP 服务
	fmt.Print("是否启用 HTTP 服务? (y/N): ")
	webInput, _ := reader.ReadString('\n')
	web = strings.ToLower(strings.TrimSpace(webInput)) == "y"

	// gRPC 服务
	fmt.Print("是否启用 gRPC 服务? (y/N): ")
	grpcInput, _ := reader.ReadString('\n')
	grpc = strings.ToLower(strings.TrimSpace(grpcInput)) == "y"

	// 定时任务服务
	fmt.Print("是否启用定时任务服务? (y/N): ")
	cronInput, _ := reader.ReadString('\n')
	cron = strings.ToLower(strings.TrimSpace(cronInput)) == "y"

	// 至少选择一个服务
	if !web && !grpc && !cron {
		fmt.Println("❌ 至少需要选择一个服务类型")
		return customServiceSelection()
	}

	// 显示选择结果
	fmt.Printf("\n✅ 已选择服务类型:")
	if web {
		fmt.Print(" HTTP")
	}
	if grpc {
		fmt.Print(" gRPC")
	}
	if cron {
		fmt.Print(" Cron")
	}
	fmt.Println()

	return web, grpc, cron, nil
}
