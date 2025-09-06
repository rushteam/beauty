package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rushteam/beauty/tools/internal/buf"
	"github.com/rushteam/beauty/tools/internal/entity"
	"github.com/rushteam/beauty/tools/internal/parser"
	"github.com/rushteam/beauty/tools/internal/parser/ast"
	"github.com/rushteam/beauty/tools/internal/parser/protobuf"
	"github.com/urfave/cli/v2"
)

// Action 重构后的API命令，支持protobuf解析
func Action(c *cli.Context) error {
	args := c.Args()
	if args.Len() == 0 {
		return cli.Exit(fmt.Errorf("❌ 缺少项目名称\n\n使用示例:\n  beauty api my-project\n  beauty api /path/to/project"), 1)
	}

	// CLI 选项
	generate := c.Bool("generate")
	outDir := c.String("out")
	openapi := c.Bool("openapi")
	asJSON := c.Bool("json")
	offline := c.Bool("offline")
	verbose := c.Bool("verbose")

	// 设置项目名称
	if n := args.Get(0); len(n) > 0 {
		entity.Config.Name = n
	}

	// 获取绝对路径
	if entity.Config.Path == "" {
		pwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("❌ 获取当前目录失败: %w", err)
		}
		entity.Config.Path = filepath.Join(pwd, entity.Config.Name)
	} else {
		path, err := filepath.Abs(entity.Config.Path)
		if err != nil {
			return fmt.Errorf("❌ 获取绝对路径失败: %w", err)
		}
		entity.Config.Path = path
	}

	// 检查项目目录是否存在
	if _, err := os.Stat(entity.Config.Path); os.IsNotExist(err) {
		return cli.Exit(fmt.Errorf("❌ 项目目录不存在: %s\n\n💡 提示: 请确保目录存在或使用正确的路径", entity.Config.Path), 1)
	}

	if verbose {
		fmt.Printf("🔍 项目路径: %s\n", entity.Config.Path)
		fmt.Printf("🔍 输出目录: %s\n", outDir)
		fmt.Printf("🔍 生成模式: %t\n", generate)
		fmt.Printf("🔍 OpenAPI: %t\n", openapi)
		fmt.Printf("🔍 离线模式: %t\n", offline)
	}

	// 显示开始信息
	fmt.Println("🚀 开始解析API定义...")
	startTime := time.Now()

	// 尝试解析protobuf文件
	files, err := parseProtobufFiles(entity.Config.Path, generate, outDir, openapi, offline, verbose)
	if err != nil {
		// 如果protobuf解析失败，尝试解析传统的api.spec文件
		fmt.Printf("⚠️  protobuf解析失败，尝试解析传统格式: %v\n", err)
		return parseTraditionalSpec(entity.Config.Path, verbose)
	}

	// 输出JSON格式
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(files)
	}

	// 显示完成信息
	duration := time.Since(startTime)
	fmt.Printf("\n✅ 解析完成! 耗时: %v\n", duration.Round(time.Millisecond))

	return nil
}

// parseProtobufFiles 解析protobuf文件
func parseProtobufFiles(projectPath string, generate bool, outDir string, openapi bool, offline bool, verbose bool) ([]*ast.ProtobufFile, error) {
	fmt.Println("📡 开始解析protobuf文件...")

	// 首先尝试直接使用protobuf解析器
	if files, err := parseProtobufDirectly(projectPath, generate, outDir, openapi, offline, verbose); err == nil {
		return files, nil
	}

	// 如果直接解析失败，尝试使用buf工具
	fmt.Println("⚠️  直接解析失败，尝试使用buf工具...")

	// 创建buf管理器
	bufManager := buf.NewManager(projectPath)

	// 初始化buf配置
	if err := bufManager.Init(); err != nil {
		return nil, fmt.Errorf("❌ 初始化buf配置失败: %w\n\n💡 提示: 请确保已安装buf工具", err)
	}

	// 检查protobuf文件
	if err := bufManager.Lint(); err != nil {
		return nil, fmt.Errorf("❌ protobuf文件检查失败: %w\n\n💡 提示: 请检查.proto文件语法", err)
	}

	// 解析protobuf文件
	protobufFiles, err := bufManager.ParseProtobufFiles()
	if err != nil {
		return nil, fmt.Errorf("❌ 解析protobuf文件失败: %w", err)
	}

	// 输出解析结果
	fmt.Printf("✅ 成功解析 %d 个protobuf文件:\n", len(protobufFiles))
	for _, file := range protobufFiles {
		fmt.Printf("\n📄 文件: %s\n", file.Filename)
		fmt.Printf("   📦 包名: %s\n", file.Package)
		fmt.Printf("   🐹 Go包名: %s\n", file.GoPackage)
		fmt.Printf("   🔧 服务数量: %d\n", len(file.Services))
		fmt.Printf("   📝 消息数量: %d\n", len(file.Messages))

		// 输出服务信息
		for _, service := range file.Services {
			fmt.Printf("   🚀 服务: %s\n", service.Name)
			for _, rpc := range service.RPCs {
				fmt.Printf("     🔌 RPC: %s(%s) -> %s\n", rpc.Name, rpc.Request, rpc.Response)
			}
		}

		// 输出消息信息
		for _, message := range file.Messages {
			fmt.Printf("   📋 消息: %s\n", message.Name)
			for _, field := range message.Fields {
				fmt.Printf("     🏷️  字段: %s %s %s\n", field.Type, field.Name, field.Tag)
			}
		}
	}

	// 非交互式生成
	if generate {
		fmt.Println("\n🔨 开始生成代码...")

		// 使用新的代码生成系统
		genService := NewCodeGenerationService()
		genOptions := NewGenerateOptions().
			SetOutputDir(outDir).
			SetModuleName(entity.Config.Name).
			SetVerbose(verbose)

		// 设置生成类型
		generateTypes := []string{"api", "service"}
		if openapi {
			generateTypes = append(generateTypes, "docs")
		}
		genOptions.SetGenerateTypes(generateTypes)

		// 生成代码
		if err := genService.GenerateFromProtobuf(context.Background(), protobufFiles, genOptions); err != nil {
			return nil, fmt.Errorf("❌ 代码生成失败: %w", err)
		}
		fmt.Println("✅ 代码生成完成!")
	}

	return protobufFiles, nil
}

// parseProtobufDirectly 直接使用protobuf解析器解析文件
func parseProtobufDirectly(projectPath string, generate bool, outDir string, openapi bool, offline bool, verbose bool) ([]*ast.ProtobufFile, error) {
	// 创建grpc-gateway解析器（使用buf+描述符反射）
	parser := protobuf.NewGrpcGatewayParser(projectPath)
	parser.SetOffline(offline)

	// 解析目录中的所有protobuf文件
	files, err := parser.ParseDirectory(projectPath)
	if err != nil {
		return nil, fmt.Errorf("❌ 解析protobuf目录失败: %w", err)
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("❌ 未找到protobuf文件\n\n💡 提示: 请确保目录中包含.proto文件")
	}

	// 输出解析结果
	fmt.Printf("✅ 成功解析 %d 个protobuf文件:\n", len(files))
	for _, file := range files {
		fmt.Printf("\n📄 文件: %s\n", file.Filename)
		fmt.Printf("   📦 包名: %s\n", file.Package)
		fmt.Printf("   🐹 Go包名: %s\n", file.GoPackage)
		fmt.Printf("   🔧 服务数量: %d\n", len(file.Services))
		fmt.Printf("   📝 消息数量: %d\n", len(file.Messages))

		// 输出服务信息
		for _, service := range file.Services {
			fmt.Printf("   🚀 服务: %s\n", service.Name)
			for _, rpc := range service.RPCs {
				fmt.Printf("     🔌 RPC: %s(%s) -> %s\n", rpc.Name, rpc.Request, rpc.Response)
				// 输出HTTP选项（使用google.api.http注解）
				if rpc.HTTPOptions != nil {
					fmt.Printf("       🌐 HTTP: %s %s", rpc.HTTPOptions.Method, rpc.HTTPOptions.Path)
					if rpc.HTTPOptions.Body != "" {
						fmt.Printf(" (body: %s)", rpc.HTTPOptions.Body)
					}
					if rpc.HTTPOptions.ResponseBody != "" {
						fmt.Printf(" (response_body: %s)", rpc.HTTPOptions.ResponseBody)
					}
					fmt.Println()
					for _, add := range rpc.HTTPOptions.Additional {
						fmt.Printf("         ➕ %s %s", add.Method, add.Path)
						if add.ResponseBody != "" {
							fmt.Printf(" (response_body: %s)", add.ResponseBody)
						}
						if add.Body != "" {
							fmt.Printf(" (body: %s)", add.Body)
						}
						fmt.Println()
					}
				}
			}
		}

		// 输出消息信息
		for _, message := range file.Messages {
			fmt.Printf("   📋 消息: %s\n", message.Name)
			for _, field := range message.Fields {
				fmt.Printf("     🏷️  字段: %s %s %s\n", field.Type, field.Name, field.Tag)
			}
		}
	}

	fmt.Println("✅ protobuf解析完成!")

	// 非交互式生成
	if generate {
		// outDir 可以是相对 projectPath 的路径
		if !filepath.IsAbs(outDir) {
			outDir = filepath.Join(projectPath, outDir)
		}
		fmt.Printf("🔨 正在生成代码到目录: %s\n", outDir)

		// 使用新的代码生成系统
		genService := NewCodeGenerationService()
		genOptions := NewGenerateOptions().
			SetOutputDir(outDir).
			SetModuleName(entity.Config.Name).
			SetVerbose(verbose)

		// 设置生成类型
		generateTypes := []string{"api", "service"}
		if openapi {
			generateTypes = append(generateTypes, "docs")
		}
		genOptions.SetGenerateTypes(generateTypes)

		// 生成代码
		if err := genService.GenerateFromProtobuf(context.Background(), files, genOptions); err != nil {
			return nil, fmt.Errorf("❌ 生成代码失败: %w", err)
		}
		fmt.Println("✅ 代码生成完成!")
	}

	return files, nil
}

// parseTraditionalSpec 解析传统的api.spec文件
func parseTraditionalSpec(projectPath string, verbose bool) error {
	specPath := filepath.Join(projectPath, "api.spec")

	// 检查api.spec文件是否存在
	if _, err := os.Stat(specPath); os.IsNotExist(err) {
		return cli.Exit(fmt.Errorf("❌ 未找到api.spec文件: %s\n\n💡 提示: 请确保api.spec文件存在或使用protobuf格式", specPath), 1)
	}

	// 读取spec文件
	spec, err := os.ReadFile(specPath)
	if err != nil {
		return cli.Exit(fmt.Errorf("❌ 读取api.spec文件失败: %w", err), 1)
	}

	fmt.Println("📄 解析传统api.spec文件:")
	if verbose {
		fmt.Println(string(spec))
	}

	// 使用现有的解析器解析
	content := string(spec)
	stmts, err := parser.Parser(strings.NewReader(content), "")
	if err != nil {
		return cli.Exit(fmt.Errorf("❌ 解析api.spec失败: %w\n\n💡 提示: 请检查api.spec文件格式", err), 1)
	}

	// 输出解析结果
	fmt.Printf("✅ 成功解析 %d 个语句:\n", len(stmts))
	for _, stmt := range stmts {
		for _, service := range stmt.Services {
			fmt.Printf("🚀 服务: %s\n", service.Name)
			for _, rpc := range service.Rpcs {
				fmt.Printf("  🔌 RPC: %s(%s) -> %s\n", rpc.Handler, rpc.Request, rpc.Response)
				for _, route := range rpc.Routes {
					fmt.Printf("    🌐 路由: %s %s\n", strings.Join(route.Methods, "|"), route.URI)
				}
			}
		}
	}

	return nil
}
