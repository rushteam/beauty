package new

import (
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
	"github.com/urfave/cli/v2"
)

// Action 创建新项目的命令处理函数
func Action(c *cli.Context) error {
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

	// 设置项目配置
	entity.Config.Name = projectName
	entity.Config.Template = template
	entity.Config.WithDocker = withDocker
	entity.Config.WithK8s = withK8s

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

	// 获取模块信息
	if hi, err := here.Dir(conf.Path); err == nil {
		conf.Info = hi
		if len(hi.ImportPath) > 0 {
			conf.Module = hi.ImportPath
		}
		conf.ImportPath = conf.Module + "/"
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
	case "full-stack":
		return createFullStack(conf, verbose)
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
	// TODO: 实现gRPC服务模板
	return buildProject(conf, verbose)
}

// createCronService 创建定时任务服务
func createCronService(conf *entity.Project, verbose bool) error {
	fmt.Println("⏰ 创建定时任务服务...")
	// TODO: 实现定时任务服务模板
	return buildProject(conf, verbose)
}

// createFullStack 创建完整微服务栈
func createFullStack(conf *entity.Project, verbose bool) error {
	fmt.Println("🏗️  创建完整微服务栈...")
	// TODO: 实现完整微服务栈模板
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
	tpl := tpls.Root()

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
