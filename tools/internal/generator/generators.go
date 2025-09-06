package generator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BaseGenerator 基础生成器
type BaseGenerator struct {
	name             string
	supportedFormats []string
	templateEngine   TemplateEngine
	fileGenerator    FileGenerator
	formatter        CodeFormatter
}

// NewBaseGenerator 创建基础生成器
func NewBaseGenerator(name string, supportedFormats []string) *BaseGenerator {
	return &BaseGenerator{
		name:             name,
		supportedFormats: supportedFormats,
	}
}

// GetName 获取生成器名称
func (g *BaseGenerator) GetName() string {
	return g.name
}

// GetSupportedFormats 获取支持的格式
func (g *BaseGenerator) GetSupportedFormats() []string {
	return g.supportedFormats
}

// SetTemplateEngine 设置模板引擎
func (g *BaseGenerator) SetTemplateEngine(engine TemplateEngine) {
	g.templateEngine = engine
}

// SetFileGenerator 设置文件生成器
func (g *BaseGenerator) SetFileGenerator(fg FileGenerator) {
	g.fileGenerator = fg
}

// SetFormatter 设置格式化器
func (g *BaseGenerator) SetFormatter(formatter CodeFormatter) {
	g.formatter = formatter
}

// GoAPIGenerator Go API生成器
type GoAPIGenerator struct {
	*BaseGenerator
}

// NewGoAPIGenerator 创建Go API生成器
func NewGoAPIGenerator() *GoAPIGenerator {
	return &GoAPIGenerator{
		BaseGenerator: NewBaseGenerator("go-api", []string{"protobuf", "openapi", "api-spec"}),
	}
}

// GenerateAPI 生成API代码
func (g *GoAPIGenerator) GenerateAPI(ctx context.Context, spec *APISpec, options *GenerateOptions) error {
	if !g.shouldGenerate("api", options) {
		return nil
	}

	fmt.Printf("🔨 生成Go API代码...\n")

	// 生成API结构体
	if err := g.generateAPIStructs(spec, options); err != nil {
		return fmt.Errorf("生成API结构体失败: %w", err)
	}

	// 生成路由
	if err := g.generateRoutes(spec, options); err != nil {
		return fmt.Errorf("生成路由失败: %w", err)
	}

	// 生成处理器
	if err := g.generateHandlers(spec, options); err != nil {
		return fmt.Errorf("生成处理器失败: %w", err)
	}

	return nil
}

// GenerateClient 生成客户端代码
func (g *GoAPIGenerator) GenerateClient(ctx context.Context, spec *APISpec, options *GenerateOptions) error {
	if !g.shouldGenerate("client", options) {
		return nil
	}

	fmt.Printf("🔨 生成Go客户端代码...\n")

	// 生成客户端结构体
	if err := g.generateClientStructs(spec, options); err != nil {
		return fmt.Errorf("生成客户端结构体失败: %w", err)
	}

	// 生成客户端方法
	if err := g.generateClientMethods(spec, options); err != nil {
		return fmt.Errorf("生成客户端方法失败: %w", err)
	}

	return nil
}

// GenerateTests 生成测试代码
func (g *GoAPIGenerator) GenerateTests(ctx context.Context, spec *APISpec, options *GenerateOptions) error {
	if !g.shouldGenerate("tests", options) {
		return nil
	}

	fmt.Printf("🔨 生成Go测试代码...\n")

	// 生成单元测试
	if err := g.generateUnitTests(spec, options); err != nil {
		return fmt.Errorf("生成单元测试失败: %w", err)
	}

	// 生成集成测试
	if err := g.generateIntegrationTests(spec, options); err != nil {
		return fmt.Errorf("生成集成测试失败: %w", err)
	}

	return nil
}

// GenerateDocs 生成文档
func (g *GoAPIGenerator) GenerateDocs(ctx context.Context, spec *APISpec, options *GenerateOptions) error {
	if !g.shouldGenerate("docs", options) {
		return nil
	}

	fmt.Printf("🔨 生成文档...\n")

	// 生成API文档
	if err := g.generateAPIDocs(spec, options); err != nil {
		return fmt.Errorf("生成API文档失败: %w", err)
	}

	// 生成README
	if err := g.generateREADME(spec, options); err != nil {
		return fmt.Errorf("生成README失败: %w", err)
	}

	return nil
}

// GenerateService 生成服务代码
func (g *GoAPIGenerator) GenerateService(ctx context.Context, spec *APISpec, options *GenerateOptions) error {
	if !g.shouldGenerate("service", options) {
		return nil
	}

	fmt.Printf("🔨 生成Go服务代码...\n")

	// 生成服务接口
	if err := g.generateServiceInterfaces(spec, options); err != nil {
		return fmt.Errorf("生成服务接口失败: %w", err)
	}

	// 生成服务实现
	if err := g.generateServiceImplementations(spec, options); err != nil {
		return fmt.Errorf("生成服务实现失败: %w", err)
	}

	return nil
}

// GenerateMiddleware 生成中间件代码
func (g *GoAPIGenerator) GenerateMiddleware(ctx context.Context, spec *APISpec, options *GenerateOptions) error {
	if !g.shouldGenerate("middleware", options) {
		return nil
	}

	fmt.Printf("🔨 生成Go中间件代码...\n")

	// 生成中间件
	if err := g.generateMiddleware(spec, options); err != nil {
		return fmt.Errorf("生成中间件失败: %w", err)
	}

	return nil
}

// shouldGenerate 判断是否应该生成指定类型的代码
func (g *GoAPIGenerator) shouldGenerate(generateType string, options *GenerateOptions) bool {
	for _, t := range options.GenerateTypes {
		if t == generateType {
			return true
		}
	}
	return false
}

// generateAPIStructs 生成API结构体
func (g *GoAPIGenerator) generateAPIStructs(spec *APISpec, options *GenerateOptions) error {
	outputDir := filepath.Join(options.OutputDir, "api")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return err
	}

	// 生成请求/响应结构体
	for _, model := range spec.Models {
		if err := g.generateModelStruct(&model, outputDir, options); err != nil {
			return err
		}
	}

	return nil
}

// generateModelStruct 生成模型结构体
func (g *GoAPIGenerator) generateModelStruct(model *Model, outputDir string, options *GenerateOptions) error {
	fileName := strings.ToLower(model.Name) + ".go"
	filePath := filepath.Join(outputDir, fileName)

	// 这里应该使用模板引擎生成代码
	// 暂时使用简单的字符串拼接
	code := g.generateGoStruct(model, options)

	if err := os.WriteFile(filePath, []byte(code), 0644); err != nil {
		return err
	}

	if options.Verbose {
		fmt.Printf("📄 生成文件: %s\n", filePath)
	}

	return nil
}

// generateGoStruct 生成Go结构体代码
func (g *GoAPIGenerator) generateGoStruct(model *Model, options *GenerateOptions) string {
	var code strings.Builder

	code.WriteString("package api\n\n")
	code.WriteString(fmt.Sprintf("// %s %s\n", model.Name, model.Description))
	code.WriteString(fmt.Sprintf("type %s struct {\n", model.Name))

	for _, field := range model.Fields {
		code.WriteString(fmt.Sprintf("\t%s %s `json:\"%s\"`\n",
			g.toGoFieldName(field.Name),
			g.toGoType(field.Type),
			field.Name))
	}

	code.WriteString("}\n")

	return code.String()
}

// generateRoutes 生成路由
func (g *GoAPIGenerator) generateRoutes(spec *APISpec, options *GenerateOptions) error {
	outputDir := filepath.Join(options.OutputDir, "router")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return err
	}

	filePath := filepath.Join(outputDir, "routes.go")
	code := g.generateRoutesCode(spec, options)

	if err := os.WriteFile(filePath, []byte(code), 0644); err != nil {
		return err
	}

	if options.Verbose {
		fmt.Printf("📄 生成文件: %s\n", filePath)
	}

	return nil
}

// generateRoutesCode 生成路由代码
func (g *GoAPIGenerator) generateRoutesCode(spec *APISpec, options *GenerateOptions) string {
	var code strings.Builder

	code.WriteString("package router\n\n")
	code.WriteString("import (\n")
	code.WriteString("\t\"net/http\"\n")
	code.WriteString(fmt.Sprintf("\t\"%s/api\"\n", options.ModuleName))
	code.WriteString(")\n\n")
	code.WriteString("// SetupRoutes 设置路由\n")
	code.WriteString("func SetupRoutes() *http.ServeMux {\n")
	code.WriteString("\tmux := http.NewServeMux()\n\n")

	for _, endpoint := range spec.Endpoints {
		code.WriteString(fmt.Sprintf("\tmux.HandleFunc(\"%s\", %s)\n",
			endpoint.Path,
			g.toGoHandlerName(endpoint.Handler)))
	}

	code.WriteString("\n\treturn mux\n")
	code.WriteString("}\n")

	return code.String()
}

// generateHandlers 生成处理器
func (g *GoAPIGenerator) generateHandlers(spec *APISpec, options *GenerateOptions) error {
	outputDir := filepath.Join(options.OutputDir, "handlers")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return err
	}

	filePath := filepath.Join(outputDir, "handlers.go")
	code := g.generateHandlersCode(spec, options)

	if err := os.WriteFile(filePath, []byte(code), 0644); err != nil {
		return err
	}

	if options.Verbose {
		fmt.Printf("📄 生成文件: %s\n", filePath)
	}

	return nil
}

// generateHandlersCode 生成处理器代码
func (g *GoAPIGenerator) generateHandlersCode(spec *APISpec, options *GenerateOptions) string {
	var code strings.Builder

	code.WriteString("package handlers\n\n")
	code.WriteString("import (\n")
	code.WriteString("\t\"encoding/json\"\n")
	code.WriteString("\t\"net/http\"\n")
	code.WriteString(fmt.Sprintf("\t\"%s/api\"\n", options.ModuleName))
	code.WriteString(")\n\n")

	for _, endpoint := range spec.Endpoints {
		code.WriteString(fmt.Sprintf("// %s 处理%s请求\n",
			g.toGoHandlerName(endpoint.Handler),
			endpoint.Name))
		code.WriteString(fmt.Sprintf("func %s(w http.ResponseWriter, r *http.Request) {\n",
			g.toGoHandlerName(endpoint.Handler)))
		code.WriteString("\t// TODO: 实现处理器逻辑\n")
		code.WriteString("\tw.Header().Set(\"Content-Type\", \"application/json\")\n")
		code.WriteString("\tjson.NewEncoder(w).Encode(map[string]string{\"message\": \"ok\"})\n")
		code.WriteString("}\n\n")
	}

	return code.String()
}

// 其他生成方法的实现...
func (g *GoAPIGenerator) generateClientStructs(spec *APISpec, options *GenerateOptions) error {
	// 实现客户端结构体生成
	return nil
}

func (g *GoAPIGenerator) generateClientMethods(spec *APISpec, options *GenerateOptions) error {
	// 实现客户端方法生成
	return nil
}

func (g *GoAPIGenerator) generateUnitTests(spec *APISpec, options *GenerateOptions) error {
	// 实现单元测试生成
	return nil
}

func (g *GoAPIGenerator) generateIntegrationTests(spec *APISpec, options *GenerateOptions) error {
	// 实现集成测试生成
	return nil
}

func (g *GoAPIGenerator) generateAPIDocs(spec *APISpec, options *GenerateOptions) error {
	// 实现API文档生成
	return nil
}

func (g *GoAPIGenerator) generateREADME(spec *APISpec, options *GenerateOptions) error {
	// 实现README生成
	return nil
}

func (g *GoAPIGenerator) generateServiceInterfaces(spec *APISpec, options *GenerateOptions) error {
	// 实现服务接口生成
	return nil
}

func (g *GoAPIGenerator) generateServiceImplementations(spec *APISpec, options *GenerateOptions) error {
	// 实现服务实现生成
	return nil
}

func (g *GoAPIGenerator) generateMiddleware(spec *APISpec, options *GenerateOptions) error {
	// 实现中间件生成
	return nil
}

// 辅助方法
func (g *GoAPIGenerator) toGoFieldName(name string) string {
	return strings.Title(name)
}

func (g *GoAPIGenerator) toGoType(typ string) string {
	switch typ {
	case "string":
		return "string"
	case "int32":
		return "int32"
	case "int64":
		return "int64"
	case "bool":
		return "bool"
	case "float32":
		return "float32"
	case "float64":
		return "float64"
	default:
		return "interface{}"
	}
}

func (g *GoAPIGenerator) toGoHandlerName(name string) string {
	return strings.Title(name) + "Handler"
}
