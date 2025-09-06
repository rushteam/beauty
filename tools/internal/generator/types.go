package generator

import (
	"context"
	"io"
)

// CodeGenerator 代码生成器接口
type CodeGenerator interface {
	// GenerateAPI 生成API相关代码
	GenerateAPI(ctx context.Context, spec *APISpec, options *GenerateOptions) error

	// GenerateClient 生成客户端代码
	GenerateClient(ctx context.Context, spec *APISpec, options *GenerateOptions) error

	// GenerateTests 生成测试代码
	GenerateTests(ctx context.Context, spec *APISpec, options *GenerateOptions) error

	// GenerateDocs 生成文档
	GenerateDocs(ctx context.Context, spec *APISpec, options *GenerateOptions) error

	// GenerateService 生成服务实现代码
	GenerateService(ctx context.Context, spec *APISpec, options *GenerateOptions) error

	// GenerateMiddleware 生成中间件代码
	GenerateMiddleware(ctx context.Context, spec *APISpec, options *GenerateOptions) error

	// GetName 获取生成器名称
	GetName() string

	// GetSupportedFormats 获取支持的格式
	GetSupportedFormats() []string
}

// APISpec API规范
type APISpec struct {
	Name        string                 `json:"name"`
	Version     string                 `json:"version"`
	Description string                 `json:"description"`
	Endpoints   []Endpoint             `json:"endpoints"`
	Models      []Model                `json:"models"`
	Middleware  []Middleware           `json:"middleware"`
	Services    []Service              `json:"services"`
	Metadata    map[string]interface{} `json:"metadata"`
}

// Endpoint API端点
type Endpoint struct {
	Name        string            `json:"name"`
	Path        string            `json:"path"`
	Method      string            `json:"method"`
	Handler     string            `json:"handler"`
	Request     *Model            `json:"request,omitempty"`
	Response    *Model            `json:"response,omitempty"`
	Middleware  []string          `json:"middleware,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Summary     string            `json:"summary,omitempty"`
	Description string            `json:"description,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// Model 数据模型
type Model struct {
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	Fields      []Field           `json:"fields"`
	Description string            `json:"description,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// Field 模型字段
type Field struct {
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	Required    bool              `json:"required"`
	Default     interface{}       `json:"default,omitempty"`
	Description string            `json:"description,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Validation  *Validation       `json:"validation,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// Validation 字段验证规则
type Validation struct {
	Min       *int     `json:"min,omitempty"`
	Max       *int     `json:"max,omitempty"`
	Pattern   string   `json:"pattern,omitempty"`
	MinLength *int     `json:"min_length,omitempty"`
	MaxLength *int     `json:"max_length,omitempty"`
	Enum      []string `json:"enum,omitempty"`
	Custom    string   `json:"custom,omitempty"`
}

// Middleware 中间件
type Middleware struct {
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	Priority    int               `json:"priority"`
	Config      map[string]string `json:"config,omitempty"`
	Description string            `json:"description,omitempty"`
}

// Service 服务定义
type Service struct {
	Name        string            `json:"name"`
	Type        string            `json:"type"` // http, grpc, cron, etc.
	Endpoints   []Endpoint        `json:"endpoints,omitempty"`
	Config      map[string]string `json:"config,omitempty"`
	Description string            `json:"description,omitempty"`
}

// GenerateOptions 生成选项
type GenerateOptions struct {
	OutputDir     string            `json:"output_dir"`
	TemplateDir   string            `json:"template_dir,omitempty"`
	Language      string            `json:"language"`  // go, typescript, python, etc.
	Framework     string            `json:"framework"` // gin, chi, echo, etc.
	PackageName   string            `json:"package_name"`
	ModuleName    string            `json:"module_name"`
	GenerateTypes []string          `json:"generate_types"` // api, client, tests, docs, service, middleware
	Features      []string          `json:"features"`       // openapi, swagger, validation, etc.
	Config        map[string]string `json:"config,omitempty"`
	Verbose       bool              `json:"verbose"`
	DryRun        bool              `json:"dry_run"`
}

// GeneratorRegistry 生成器注册表
type GeneratorRegistry struct {
	generators map[string]CodeGenerator
}

// NewGeneratorRegistry 创建新的生成器注册表
func NewGeneratorRegistry() *GeneratorRegistry {
	return &GeneratorRegistry{
		generators: make(map[string]CodeGenerator),
	}
}

// Register 注册生成器
func (r *GeneratorRegistry) Register(generator CodeGenerator) {
	r.generators[generator.GetName()] = generator
}

// Get 获取生成器
func (r *GeneratorRegistry) Get(name string) (CodeGenerator, bool) {
	generator, exists := r.generators[name]
	return generator, exists
}

// List 列出所有生成器
func (r *GeneratorRegistry) List() []CodeGenerator {
	var generators []CodeGenerator
	for _, generator := range r.generators {
		generators = append(generators, generator)
	}
	return generators
}

// GenerateAll 使用所有支持的生成器生成代码
func (r *GeneratorRegistry) GenerateAll(ctx context.Context, spec *APISpec, options *GenerateOptions) error {
	for _, generator := range r.generators {
		if err := r.generateWithGenerator(ctx, generator, spec, options); err != nil {
			return err
		}
	}
	return nil
}

// generateWithGenerator 使用指定生成器生成代码
func (r *GeneratorRegistry) generateWithGenerator(ctx context.Context, generator CodeGenerator, spec *APISpec, options *GenerateOptions) error {
	for _, generateType := range options.GenerateTypes {
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
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// TemplateEngine 模板引擎接口
type TemplateEngine interface {
	// Render 渲染模板
	Render(templateName string, data interface{}, output io.Writer) error

	// RenderFile 渲染文件模板
	RenderFile(templatePath string, data interface{}, outputPath string) error

	// RenderString 渲染字符串模板
	RenderString(template string, data interface{}) (string, error)

	// LoadTemplates 加载模板
	LoadTemplates(templateDir string) error
}

// FileGenerator 文件生成器接口
type FileGenerator interface {
	// GenerateFile 生成单个文件
	GenerateFile(ctx context.Context, spec *APISpec, options *GenerateOptions, filePath string) error

	// GenerateDirectory 生成目录结构
	GenerateDirectory(ctx context.Context, spec *APISpec, options *GenerateOptions, dirPath string) error

	// GetTemplatePath 获取模板路径
	GetTemplatePath(templateName string) string
}

// CodeFormatter 代码格式化器接口
type CodeFormatter interface {
	// Format 格式化代码
	Format(code string, language string) (string, error)

	// FormatFile 格式化文件
	FormatFile(filePath string, language string) error
}

// Validator 验证器接口
type Validator interface {
	// ValidateSpec 验证API规范
	ValidateSpec(spec *APISpec) error

	// ValidateOptions 验证生成选项
	ValidateOptions(options *GenerateOptions) error
}
