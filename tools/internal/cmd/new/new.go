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

// Action 创建新项目或向现有项目添加服务的命令处理函数
func Action(ctx context.Context, c *cli.Command) error {
	args := c.Args()
	if args.Len() == 0 {
		return cli.Exit(fmt.Errorf("❌ 缺少项目名称或路径\n\n使用示例:\n  beauty new my-project\n  beauty new my-project --template grpc-service\n  beauty new . --grpc  # 在当前目录添加gRPC服务"), 1)
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

	// 设置项目路径
	var targetPath string
	if projectPath != "" {
		// 使用指定的路径
		path, err := filepath.Abs(projectPath)
		if err != nil {
			return fmt.Errorf("❌ 获取绝对路径失败: %w", err)
		}
		targetPath = path
	} else if projectName == "." || projectName == "./" {
		// 使用当前目录
		pwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("❌ 获取当前目录失败: %w", err)
		}
		targetPath = pwd
	} else {
		// 创建新目录
		pwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("❌ 获取当前目录失败: %w", err)
		}
		targetPath = filepath.Join(pwd, projectName)
	}

	entity.Config.Path = targetPath

	// 检查项目目录是否已存在
	dirExists := false
	if _, err := os.Stat(entity.Config.Path); !os.IsNotExist(err) {
		dirExists = true
	}

	// 处理服务类型选择
	if template == "unified" {
		// 交互式选择服务类型
		if !enableWeb && !enableGrpc && !enableCron {
			// 如果没有通过命令行指定，则进行交互式选择
			var existingServices *ProjectServices
			if dirExists {
				// 检测现有服务
				detector := NewProjectDetector(entity.Config.Path)
				var err error
				existingServices, err = detector.DetectServices()
				if err != nil {
					return fmt.Errorf("❌ 检测现有服务失败: %w", err)
				}
			}

			web, grpc, cron, err := interactiveServiceSelection(existingServices)
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
		fmt.Printf("🔍 项目名称: %s\n", projectName)
		fmt.Printf("🔍 项目路径: %s\n", entity.Config.Path)
		fmt.Printf("🔍 模板类型: %s\n", template)
		fmt.Printf("🔍 包含Docker: %t\n", withDocker)
		fmt.Printf("🔍 包含K8s: %t\n", withK8s)
	}

	// 显示开始信息
	if dirExists {
		fmt.Println("🔧 检测到现有项目，开始添加服务...")
	} else {
		fmt.Println("🚀 开始创建Beauty项目...")
	}
	startTime := time.Now()

	// 创建项目或添加服务
	if err := createOrUpdateProject(entity.Config, dirExists, verbose); err != nil {
		return fmt.Errorf("❌ 操作失败: %w", err)
	}

	// 显示完成信息
	duration := time.Since(startTime)
	if dirExists {
		fmt.Printf("\n✅ 服务添加完成! 耗时: %v\n", duration.Round(time.Millisecond))
	} else {
		fmt.Printf("\n✅ 项目创建完成! 耗时: %v\n", duration.Round(time.Millisecond))
	}

	// 显示后续步骤
	fmt.Println("\n📋 后续步骤:")
	if !dirExists {
		fmt.Printf("  cd %s\n", projectName)
	}
	fmt.Println("  go mod tidy")
	fmt.Println("  go run main.go")

	if withDocker {
		fmt.Println("  docker build -t " + projectName + " .")
		fmt.Println("  docker run -p 8080:8080 " + projectName)
	}

	return nil
}

// ProjectServices 项目服务检测结果
type ProjectServices struct {
	Web  bool
	Grpc bool
	Cron bool
}

// HasWeb 检查是否有Web服务
func (ps *ProjectServices) HasWeb() bool {
	return ps.Web
}

// HasGrpc 检查是否有gRPC服务
func (ps *ProjectServices) HasGrpc() bool {
	return ps.Grpc
}

// HasCron 检查是否有Cron服务
func (ps *ProjectServices) HasCron() bool {
	return ps.Cron
}

// ProjectDetector 项目结构检测器
type ProjectDetector struct {
	projectPath string
}

// NewProjectDetector 创建项目检测器
func NewProjectDetector(projectPath string) *ProjectDetector {
	return &ProjectDetector{
		projectPath: projectPath,
	}
}

// DetectServices 检测现有服务类型
func (pd *ProjectDetector) DetectServices() (*ProjectServices, error) {
	services := &ProjectServices{}

	// 检测Web服务
	if pd.hasWebService() {
		services.Web = true
	}

	// 检测gRPC服务
	if pd.hasGrpcService() {
		services.Grpc = true
	}

	// 检测Cron服务
	if pd.hasCronService() {
		services.Cron = true
	}

	return services, nil
}

// hasWebService 检测是否有Web服务
func (pd *ProjectDetector) hasWebService() bool {
	// 检查是否存在HTTP相关的文件
	webIndicators := []string{
		"internal/endpoint/handlers",
		"internal/endpoint/router",
		"internal/infra/middleware",
	}

	for _, indicator := range webIndicators {
		path := filepath.Join(pd.projectPath, indicator)
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}

	// 检查main.go中是否有HTTP服务相关代码
	mainPath := filepath.Join(pd.projectPath, "main.go")
	if content, err := os.ReadFile(mainPath); err == nil {
		contentStr := string(content)
		if strings.Contains(contentStr, "webserver") || strings.Contains(contentStr, "http") {
			return true
		}
	}

	return false
}

// hasGrpcService 检测是否有gRPC服务
func (pd *ProjectDetector) hasGrpcService() bool {
	// 检查是否存在gRPC相关的文件
	grpcIndicators := []string{
		"internal/endpoint/grpc",
		"internal/service",
		"api",
		"buf.yaml",
		"buf.gen.yaml",
	}

	for _, indicator := range grpcIndicators {
		path := filepath.Join(pd.projectPath, indicator)
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}

	// 检查main.go中是否有gRPC服务相关代码
	mainPath := filepath.Join(pd.projectPath, "main.go")
	if content, err := os.ReadFile(mainPath); err == nil {
		contentStr := string(content)
		if strings.Contains(contentStr, "grpcserver") || strings.Contains(contentStr, "grpc") {
			return true
		}
	}

	return false
}

// hasCronService 检测是否有Cron服务
func (pd *ProjectDetector) hasCronService() bool {
	// 检查是否存在Cron相关的文件
	cronIndicators := []string{
		"internal/endpoint/job",
		"internal/job",
	}

	for _, indicator := range cronIndicators {
		path := filepath.Join(pd.projectPath, indicator)
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}

	// 检查main.go中是否有Cron服务相关代码
	mainPath := filepath.Join(pd.projectPath, "main.go")
	if content, err := os.ReadFile(mainPath); err == nil {
		contentStr := string(content)
		if strings.Contains(contentStr, "cron") || strings.Contains(contentStr, "job") {
			return true
		}
	}

	return false
}

// GetProjectInfo 获取项目信息
func (pd *ProjectDetector) GetProjectInfo() (*entity.Project, error) {
	// 读取go.mod文件获取模块名
	goModPath := filepath.Join(pd.projectPath, "go.mod")
	var moduleName string
	if content, err := os.ReadFile(goModPath); err == nil {
		lines := strings.Split(string(content), "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "module ") {
				moduleName = strings.TrimSpace(strings.TrimPrefix(line, "module "))
				break
			}
		}
	}

	// 如果没有找到模块名，使用目录名
	if moduleName == "" {
		moduleName = filepath.Base(pd.projectPath)
	}

	return &entity.Project{
		Name:       moduleName,
		Module:     moduleName,
		Path:       pd.projectPath,
		ImportPath: moduleName + "/",
		Template:   "unified", // 使用unified模板来支持多种服务
	}, nil
}

// createOrUpdateProject 创建新项目或更新现有项目
func createOrUpdateProject(conf *entity.Project, dirExists bool, verbose bool) error {
	if dirExists {
		return updateExistingProject(conf, verbose)
	}
	return createProject(conf, verbose)
}

// updateExistingProject 更新现有项目
func updateExistingProject(conf *entity.Project, verbose bool) error {
	// 检测现有项目结构
	detector := NewProjectDetector(conf.Path)
	existingServices, err := detector.DetectServices()
	if err != nil {
		return fmt.Errorf("检测项目结构失败: %w", err)
	}

	if verbose {
		fmt.Printf("🔍 检测到的现有服务: %v\n", existingServices)
	}

	// 确定需要添加的服务
	var servicesToAdd []string
	if conf.EnableWeb && !existingServices.HasWeb() {
		servicesToAdd = append(servicesToAdd, "web")
	}
	if conf.EnableGrpc && !existingServices.HasGrpc() {
		servicesToAdd = append(servicesToAdd, "grpc")
	}
	if conf.EnableCron && !existingServices.HasCron() {
		servicesToAdd = append(servicesToAdd, "cron")
	}

	if len(servicesToAdd) == 0 {
		fmt.Println("✅ 所有请求的服务类型都已存在，无需添加")
		return nil
	}

	// 显示将要添加的服务
	fmt.Printf("📋 将添加的服务: %s\n", strings.Join(servicesToAdd, ", "))

	// 获取项目信息
	projectInfo, err := detector.GetProjectInfo()
	if err != nil {
		return fmt.Errorf("获取项目信息失败: %w", err)
	}

	// 添加服务
	generator := NewServiceGenerator(conf.Path, projectInfo)
	for _, serviceType := range servicesToAdd {
		if err := generator.AddService(serviceType, verbose); err != nil {
			return fmt.Errorf("添加 %s 服务失败: %w", serviceType, err)
		}
	}

	return nil
}

// ServiceGenerator 服务生成器
type ServiceGenerator struct {
	projectPath string
	projectInfo *entity.Project
}

// NewServiceGenerator 创建服务生成器
func NewServiceGenerator(projectPath string, projectInfo *entity.Project) *ServiceGenerator {
	return &ServiceGenerator{
		projectPath: projectPath,
		projectInfo: projectInfo,
	}
}

// AddService 添加指定类型的服务
func (sg *ServiceGenerator) AddService(serviceType string, verbose bool) error {
	fmt.Printf("🔧 添加 %s 服务...\n", serviceType)

	// 获取对应的模板
	var templateFS fs.FS
	switch serviceType {
	case "web":
		templateFS = tpls.Root()
	case "grpc":
		templateFS = tpls.GrpcRoot()
	case "cron":
		templateFS = tpls.CronRoot()
	default:
		return fmt.Errorf("不支持的服务类型: %s", serviceType)
	}

	// 生成服务文件
	return sg.generateServiceFiles(templateFS, serviceType, verbose)
}

// generateServiceFiles 生成服务文件
func (sg *ServiceGenerator) generateServiceFiles(templateFS fs.FS, serviceType string, verbose bool) error {
	return fs.WalkDir(templateFS, ".", func(path string, info os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// 跳过不需要的文件
		if sg.shouldSkipFile(path, serviceType) {
			return nil
		}

		if info.IsDir() {
			dirPath := filepath.Join(sg.projectPath, path)
			if err := pkg.MkdirAll(dirPath); err != nil {
				return err
			}
			if verbose {
				fmt.Printf("📁 创建目录: %s\n", dirPath)
			}
			return nil
		}

		// 读取模板文件
		src, err := templateFS.Open(path)
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
		outputPath := filepath.Join(sg.projectPath, filename)

		// 检查文件是否已存在
		if _, err := os.Stat(outputPath); err == nil {
			if verbose {
				fmt.Printf("⚠️  文件已存在，跳过: %s\n", outputPath)
			}
			return nil
		}

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

		if err := tmpl.Execute(dst, sg.projectInfo); err != nil {
			return err
		}

		if verbose {
			fmt.Printf("📄 创建文件: %s\n", outputPath)
		}

		return nil
	})
}

// shouldSkipFile 判断是否应该跳过某个文件
func (sg *ServiceGenerator) shouldSkipFile(path string, serviceType string) bool {
	// 跳过一些通用文件，避免覆盖现有文件
	skipFiles := []string{
		"go.mod.tpl",
		"main.go.tpl",
		"config/dev/app.yaml.tpl",
	}

	for _, skipFile := range skipFiles {
		if strings.HasSuffix(path, skipFile) {
			return true
		}
	}

	return false
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
		if !conf.EnableCron && (strings.Contains(path, "cron") || strings.Contains(path, "endpoint/job")) {
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
func interactiveServiceSelection(existingServices *ProjectServices) (web, grpc, cron bool, err error) {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("\n🎯 请选择要启用的服务类型:")

	// 如果有现有服务，显示现有服务状态
	if existingServices != nil {
		fmt.Println("\n📋 现有服务:")
		if existingServices.HasWeb() {
			fmt.Println("   ✅ HTTP 服务")
		} else {
			fmt.Println("   ❌ HTTP 服务")
		}
		if existingServices.HasGrpc() {
			fmt.Println("   ✅ gRPC 服务")
		} else {
			fmt.Println("   ❌ gRPC 服务")
		}
		if existingServices.HasCron() {
			fmt.Println("   ✅ 定时任务服务")
		} else {
			fmt.Println("   ❌ 定时任务服务")
		}
	}

	fmt.Println("\n🔧 可添加的服务:")
	if existingServices == nil || !existingServices.HasWeb() {
		fmt.Println("   1. HTTP 服务 (REST API)")
	}
	if existingServices == nil || !existingServices.HasGrpc() {
		fmt.Println("   2. gRPC 服务 (高性能 RPC)")
	}
	if existingServices == nil || !existingServices.HasCron() {
		fmt.Println("   3. 定时任务服务 (Cron Jobs)")
	}
	if existingServices == nil {
		fmt.Println("   4. 全栈服务 (HTTP + gRPC + Cron)")
		fmt.Println("   5. 自定义组合")
	}
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
			if existingServices == nil || !existingServices.HasWeb() {
				web = true
			}
		case "2":
			if existingServices == nil || !existingServices.HasGrpc() {
				grpc = true
			}
		case "3":
			if existingServices == nil || !existingServices.HasCron() {
				cron = true
			}
		case "4":
			if existingServices == nil {
				web = true
				grpc = true
				cron = true
			}
		case "5":
			if existingServices == nil {
				// 自定义组合
				return customServiceSelection()
			}
		default:
			fmt.Printf("⚠️  无效选项: %s，已忽略\n", opt)
		}
	}

	// 至少选择一个服务
	if !web && !grpc && !cron {
		fmt.Println("❌ 至少需要选择一个服务类型")
		return interactiveServiceSelection(existingServices)
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
