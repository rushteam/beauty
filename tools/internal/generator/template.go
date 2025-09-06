package generator

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// GoTemplateEngine Go模板引擎
type GoTemplateEngine struct {
	templates map[string]*template.Template
}

// NewGoTemplateEngine 创建Go模板引擎
func NewGoTemplateEngine() *GoTemplateEngine {
	return &GoTemplateEngine{
		templates: make(map[string]*template.Template),
	}
}

// Render 渲染模板
func (e *GoTemplateEngine) Render(templateName string, data interface{}, output io.Writer) error {
	tmpl, exists := e.templates[templateName]
	if !exists {
		return fmt.Errorf("模板不存在: %s", templateName)
	}

	return tmpl.Execute(output, data)
}

// RenderFile 渲染文件模板
func (e *GoTemplateEngine) RenderFile(templatePath string, data interface{}, outputPath string) error {
	// 确保输出目录存在
	dir := filepath.Dir(outputPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// 创建输出文件
	file, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer file.Close()

	// 渲染模板
	return e.Render(templatePath, data, file)
}

// RenderString 渲染字符串模板
func (e *GoTemplateEngine) RenderString(templateStr string, data interface{}) (string, error) {
	tmpl, err := template.New("").Parse(templateStr)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}

	return buf.String(), nil
}

// LoadTemplates 加载模板
func (e *GoTemplateEngine) LoadTemplates(templateDir string) error {
	return filepath.Walk(templateDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() || !strings.HasSuffix(path, ".tpl") {
			return nil
		}

		// 读取模板文件
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		// 解析模板
		tmpl, err := template.New(filepath.Base(path)).Parse(string(content))
		if err != nil {
			return err
		}

		// 存储模板
		relPath, _ := filepath.Rel(templateDir, path)
		templateName := strings.TrimSuffix(relPath, ".tpl")
		e.templates[templateName] = tmpl

		return nil
	})
}

// GoFileGenerator Go文件生成器
type GoFileGenerator struct {
	templateEngine TemplateEngine
}

// NewGoFileGenerator 创建Go文件生成器
func NewGoFileGenerator(engine TemplateEngine) *GoFileGenerator {
	return &GoFileGenerator{
		templateEngine: engine,
	}
}

// GenerateFile 生成单个文件
func (g *GoFileGenerator) GenerateFile(ctx context.Context, spec *APISpec, options *GenerateOptions, filePath string) error {
	// 获取模板路径
	templatePath := g.GetTemplatePath(filePath)
	if templatePath == "" {
		return fmt.Errorf("未找到模板: %s", filePath)
	}

	// 准备模板数据
	data := map[string]interface{}{
		"Spec":    spec,
		"Options": options,
		"Package": options.PackageName,
		"Module":  options.ModuleName,
	}

	// 渲染文件
	return g.templateEngine.RenderFile(templatePath, data, filePath)
}

// GenerateDirectory 生成目录结构
func (g *GoFileGenerator) GenerateDirectory(ctx context.Context, spec *APISpec, options *GenerateOptions, dirPath string) error {
	// 创建目录
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return err
	}

	// 根据规范生成文件
	for _, service := range spec.Services {
		if err := g.generateServiceFiles(&service, spec, options, dirPath); err != nil {
			return err
		}
	}

	return nil
}

// GetTemplatePath 获取模板路径
func (g *GoFileGenerator) GetTemplatePath(templateName string) string {
	// 这里应该根据模板名称返回实际的模板路径
	// 暂时返回一个示例路径
	return filepath.Join("templates", templateName+".tpl")
}

// generateServiceFiles 生成服务相关文件
func (g *GoFileGenerator) generateServiceFiles(service *Service, spec *APISpec, options *GenerateOptions, baseDir string) error {
	serviceDir := filepath.Join(baseDir, strings.ToLower(service.Name))
	if err := os.MkdirAll(serviceDir, 0755); err != nil {
		return err
	}

	// 生成服务接口
	interfaceFile := filepath.Join(serviceDir, "interface.go")
	if err := g.GenerateFile(context.Background(), spec, options, interfaceFile); err != nil {
		return err
	}

	// 生成服务实现
	implFile := filepath.Join(serviceDir, "impl.go")
	if err := g.GenerateFile(context.Background(), spec, options, implFile); err != nil {
		return err
	}

	return nil
}

// GoCodeFormatter Go代码格式化器
type GoCodeFormatter struct{}

// NewGoCodeFormatter 创建Go代码格式化器
func NewGoCodeFormatter() *GoCodeFormatter {
	return &GoCodeFormatter{}
}

// Format 格式化代码
func (f *GoCodeFormatter) Format(code string, language string) (string, error) {
	if language != "go" {
		return code, nil
	}

	// 这里应该使用go fmt或gofmt来格式化代码
	// 暂时返回原代码
	return code, nil
}

// FormatFile 格式化文件
func (f *GoCodeFormatter) FormatFile(filePath string, language string) error {
	if language != "go" {
		return nil
	}

	// 这里应该使用go fmt来格式化文件
	// 暂时不做任何操作
	return nil
}

// SpecValidator 规范验证器
type SpecValidator struct{}

// NewSpecValidator 创建规范验证器
func NewSpecValidator() *SpecValidator {
	return &SpecValidator{}
}

// ValidateSpec 验证API规范
func (v *SpecValidator) ValidateSpec(spec *APISpec) error {
	if spec.Name == "" {
		return fmt.Errorf("API名称不能为空")
	}

	if spec.Version == "" {
		return fmt.Errorf("API版本不能为空")
	}

	if len(spec.Endpoints) == 0 {
		return fmt.Errorf("至少需要一个端点")
	}

	// 验证端点
	for i, endpoint := range spec.Endpoints {
		if endpoint.Name == "" {
			return fmt.Errorf("端点[%d]名称不能为空", i)
		}
		if endpoint.Path == "" {
			return fmt.Errorf("端点[%d]路径不能为空", i)
		}
		if endpoint.Method == "" {
			return fmt.Errorf("端点[%d]方法不能为空", i)
		}
	}

	// 验证模型
	for i, model := range spec.Models {
		if model.Name == "" {
			return fmt.Errorf("模型[%d]名称不能为空", i)
		}
		if len(model.Fields) == 0 {
			return fmt.Errorf("模型[%d]至少需要一个字段", i)
		}
	}

	return nil
}

// ValidateOptions 验证生成选项
func (v *SpecValidator) ValidateOptions(options *GenerateOptions) error {
	if options.OutputDir == "" {
		return fmt.Errorf("输出目录不能为空")
	}

	if options.Language == "" {
		return fmt.Errorf("语言不能为空")
	}

	if options.PackageName == "" {
		return fmt.Errorf("包名不能为空")
	}

	if len(options.GenerateTypes) == 0 {
		return fmt.Errorf("至少需要指定一种生成类型")
	}

	return nil
}
