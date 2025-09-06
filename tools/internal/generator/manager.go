package generator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CodeGenerationManager 代码生成管理器
type CodeGenerationManager struct {
	registry  *GeneratorRegistry
	validator Validator
	formatter CodeFormatter
	verbose   bool
}

// NewCodeGenerationManager 创建代码生成管理器
func NewCodeGenerationManager() *CodeGenerationManager {
	manager := &CodeGenerationManager{
		registry:  NewGeneratorRegistry(),
		validator: NewSpecValidator(),
		formatter: NewGoCodeFormatter(),
	}

	// 注册默认生成器
	manager.registerDefaultGenerators()

	return manager
}

// registerDefaultGenerators 注册默认生成器
func (m *CodeGenerationManager) registerDefaultGenerators() {
	// 注册Go API生成器
	goAPIGenerator := NewGoAPIGenerator()
	templateEngine := NewGoTemplateEngine()
	fileGenerator := NewGoFileGenerator(templateEngine)

	goAPIGenerator.SetTemplateEngine(templateEngine)
	goAPIGenerator.SetFileGenerator(fileGenerator)
	goAPIGenerator.SetFormatter(m.formatter)

	m.registry.Register(goAPIGenerator)
}

// SetVerbose 设置详细模式
func (m *CodeGenerationManager) SetVerbose(verbose bool) {
	m.verbose = verbose
}

// Generate 生成代码
func (m *CodeGenerationManager) Generate(ctx context.Context, spec *APISpec, options *GenerateOptions) error {
	// 验证规范
	if err := m.validator.ValidateSpec(spec); err != nil {
		return fmt.Errorf("规范验证失败: %w", err)
	}

	// 验证选项
	if err := m.validator.ValidateOptions(options); err != nil {
		return fmt.Errorf("选项验证失败: %w", err)
	}

	// 设置详细模式
	options.Verbose = m.verbose

	// 创建输出目录
	if err := os.MkdirAll(options.OutputDir, 0755); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}

	if m.verbose {
		fmt.Printf("🚀 开始生成代码...\n")
		fmt.Printf("📁 输出目录: %s\n", options.OutputDir)
		fmt.Printf("🔧 生成类型: %s\n", strings.Join(options.GenerateTypes, ", "))
		fmt.Printf("🌐 语言: %s\n", options.Language)
		fmt.Printf("📦 包名: %s\n", options.PackageName)
	}

	// 根据语言选择生成器
	generator, exists := m.registry.Get(options.Language + "-api")
	if !exists {
		return fmt.Errorf("不支持的语言: %s", options.Language)
	}

	// 生成代码
	if err := m.generateWithGenerator(ctx, generator, spec, options); err != nil {
		return fmt.Errorf("代码生成失败: %w", err)
	}

	if m.verbose {
		fmt.Printf("✅ 代码生成完成!\n")
	}

	return nil
}

// generateWithGenerator 使用指定生成器生成代码
func (m *CodeGenerationManager) generateWithGenerator(ctx context.Context, generator CodeGenerator, spec *APISpec, options *GenerateOptions) error {
	for _, generateType := range options.GenerateTypes {
		if m.verbose {
			fmt.Printf("🔨 生成 %s 代码...\n", generateType)
		}

		var err error
		switch generateType {
		case "api":
			err = generator.GenerateAPI(ctx, spec, options)
		case "client":
			err = generator.GenerateClient(ctx, spec, options)
		case "tests":
			err = generator.GenerateTests(ctx, spec, options)
		case "docs":
			err = generator.GenerateDocs(ctx, spec, options)
		case "service":
			err = generator.GenerateService(ctx, spec, options)
		case "middleware":
			err = generator.GenerateMiddleware(ctx, spec, options)
		default:
			return fmt.Errorf("不支持的生成类型: %s", generateType)
		}

		if err != nil {
			return fmt.Errorf("生成%s失败: %w", generateType, err)
		}
	}

	return nil
}

// GenerateFromProtobuf 从protobuf文件生成代码
func (m *CodeGenerationManager) GenerateFromProtobuf(ctx context.Context, protoFiles []string, options *GenerateOptions) error {
	// 解析protobuf文件
	spec, err := m.parseProtobufFiles(protoFiles)
	if err != nil {
		return fmt.Errorf("解析protobuf文件失败: %w", err)
	}

	// 生成代码
	return m.Generate(ctx, spec, options)
}

// GenerateFromOpenAPI 从OpenAPI规范生成代码
func (m *CodeGenerationManager) GenerateFromOpenAPI(ctx context.Context, openAPIFile string, options *GenerateOptions) error {
	// 解析OpenAPI文件
	spec, err := m.parseOpenAPIFile(openAPIFile)
	if err != nil {
		return fmt.Errorf("解析OpenAPI文件失败: %w", err)
	}

	// 生成代码
	return m.Generate(ctx, spec, options)
}

// parseProtobufFiles 解析protobuf文件
func (m *CodeGenerationManager) parseProtobufFiles(protoFiles []string) (*APISpec, error) {
	// 这里应该集成现有的protobuf解析逻辑
	// 暂时返回一个示例规范
	spec := &APISpec{
		Name:        "Example API",
		Version:     "v1",
		Description: "示例API",
		Endpoints: []Endpoint{
			{
				Name:     "CreateUser",
				Path:     "/users",
				Method:   "POST",
				Handler:  "createUser",
				Request:  &Model{Name: "CreateUserRequest"},
				Response: &Model{Name: "CreateUserResponse"},
			},
		},
		Models: []Model{
			{
				Name: "CreateUserRequest",
				Fields: []Field{
					{Name: "name", Type: "string", Required: true},
					{Name: "email", Type: "string", Required: true},
				},
			},
			{
				Name: "CreateUserResponse",
				Fields: []Field{
					{Name: "id", Type: "string", Required: true},
					{Name: "name", Type: "string", Required: true},
					{Name: "email", Type: "string", Required: true},
				},
			},
		},
	}

	return spec, nil
}

// parseOpenAPIFile 解析OpenAPI文件
func (m *CodeGenerationManager) parseOpenAPIFile(openAPIFile string) (*APISpec, error) {
	// 这里应该实现OpenAPI文件解析
	// 暂时返回一个示例规范
	return m.parseProtobufFiles([]string{})
}

// ListGenerators 列出所有生成器
func (m *CodeGenerationManager) ListGenerators() []CodeGenerator {
	return m.registry.List()
}

// GetGenerator 获取指定生成器
func (m *CodeGenerationManager) GetGenerator(name string) (CodeGenerator, bool) {
	return m.registry.Get(name)
}

// RegisterGenerator 注册生成器
func (m *CodeGenerationManager) RegisterGenerator(generator CodeGenerator) {
	m.registry.Register(generator)
}

// CreateProjectStructure 创建项目结构
func (m *CodeGenerationManager) CreateProjectStructure(options *GenerateOptions) error {
	// 创建基础目录结构
	dirs := []string{
		"api",
		"client",
		"handlers",
		"router",
		"service",
		"middleware",
		"tests",
		"docs",
	}

	for _, dir := range dirs {
		dirPath := filepath.Join(options.OutputDir, dir)
		if err := os.MkdirAll(dirPath, 0755); err != nil {
			return fmt.Errorf("创建目录失败 %s: %w", dir, err)
		}
	}

	// 创建go.mod文件
	if options.Language == "go" {
		goModPath := filepath.Join(options.OutputDir, "go.mod")
		goModContent := fmt.Sprintf("module %s\n\ngo 1.19\n", options.ModuleName)
		if err := os.WriteFile(goModPath, []byte(goModContent), 0644); err != nil {
			return fmt.Errorf("创建go.mod失败: %w", err)
		}
	}

	return nil
}

// GenerateConfig 生成配置文件
func (m *CodeGenerationManager) GenerateConfig(spec *APISpec, options *GenerateOptions) error {
	configDir := filepath.Join(options.OutputDir, "config")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return err
	}

	// 生成配置文件
	configFile := filepath.Join(configDir, "config.yaml")
	configContent := m.generateConfigContent(spec, options)
	if err := os.WriteFile(configFile, []byte(configContent), 0644); err != nil {
		return fmt.Errorf("生成配置文件失败: %w", err)
	}

	return nil
}

// generateConfigContent 生成配置内容
func (m *CodeGenerationManager) generateConfigContent(spec *APISpec, options *GenerateOptions) string {
	return fmt.Sprintf(`app: %s
version: %s
description: %s

server:
  port: 8080
  host: "0.0.0.0"

database:
  driver: "postgres"
  dsn: "postgres://user:password@localhost/dbname?sslmode=disable"

logging:
  level: "info"
  format: "json"
`, spec.Name, spec.Version, spec.Description)
}
