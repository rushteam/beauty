package api

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rushteam/beauty/tools/internal/generator"
	"github.com/rushteam/beauty/tools/internal/parser/ast"
)

// CodeGenerationService 代码生成服务
type CodeGenerationService struct {
	manager *generator.CodeGenerationManager
}

// NewCodeGenerationService 创建代码生成服务
func NewCodeGenerationService() *CodeGenerationService {
	return &CodeGenerationService{
		manager: generator.NewCodeGenerationManager(),
	}
}

// GenerateFromProtobuf 从protobuf文件生成代码
func (s *CodeGenerationService) GenerateFromProtobuf(ctx context.Context, files []*ast.ProtobufFile, options *GenerateOptions) error {
	// 转换protobuf文件为API规范
	spec := s.convertProtobufToAPISpec(files)

	// 设置生成选项
	genOptions := &generator.GenerateOptions{
		OutputDir:     options.OutputDir,
		Language:      "go",
		Framework:     "beauty",
		PackageName:   "api",
		ModuleName:    options.ModuleName,
		GenerateTypes: options.GenerateTypes,
		Features:      options.Features,
		Verbose:       options.Verbose,
		DryRun:        options.DryRun,
	}

	// 生成代码
	return s.manager.Generate(ctx, spec, genOptions)
}

// GenerateFromAPISpec 从API规范生成代码
func (s *CodeGenerationService) GenerateFromAPISpec(ctx context.Context, spec *generator.APISpec, options *GenerateOptions) error {
	// 设置生成选项
	genOptions := &generator.GenerateOptions{
		OutputDir:     options.OutputDir,
		Language:      "go",
		Framework:     "beauty",
		PackageName:   "api",
		ModuleName:    options.ModuleName,
		GenerateTypes: options.GenerateTypes,
		Features:      options.Features,
		Verbose:       options.Verbose,
		DryRun:        options.DryRun,
	}

	// 生成代码
	return s.manager.Generate(ctx, spec, genOptions)
}

// convertProtobufToAPISpec 将protobuf文件转换为API规范
func (s *CodeGenerationService) convertProtobufToAPISpec(files []*ast.ProtobufFile) *generator.APISpec {
	spec := &generator.APISpec{
		Name:        "Generated API",
		Version:     "v1",
		Description: "从protobuf文件生成的API",
		Endpoints:   []generator.Endpoint{},
		Models:      []generator.Model{},
		Services:    []generator.Service{},
		Metadata:    make(map[string]interface{}),
	}

	// 处理每个protobuf文件
	for _, file := range files {
		// 转换服务
		for _, service := range file.Services {
			apiService := generator.Service{
				Name:        service.Name,
				Type:        "grpc",
				Description: fmt.Sprintf("gRPC服务: %s", service.Name),
			}

			// 转换RPC方法为端点
			for _, rpc := range service.RPCs {
				endpoint := generator.Endpoint{
					Name:        rpc.Name,
					Path:        s.generatePathFromRPC(rpc),
					Method:      s.generateMethodFromRPC(rpc),
					Handler:     s.generateHandlerName(rpc.Name),
					Request:     s.convertMessageToModel(rpc.Request, file.Messages),
					Response:    s.convertMessageToModel(rpc.Response, file.Messages),
					Description: fmt.Sprintf("RPC方法: %s", rpc.Name),
					Tags:        []string{"grpc", service.Name},
				}

				// 添加HTTP选项
				if rpc.HTTPOptions != nil {
					endpoint.Path = rpc.HTTPOptions.Path
					endpoint.Method = strings.ToUpper(rpc.HTTPOptions.Method)
				}

				spec.Endpoints = append(spec.Endpoints, endpoint)
				apiService.Endpoints = append(apiService.Endpoints, endpoint)
			}

			spec.Services = append(spec.Services, apiService)
		}

		// 转换消息为模型
		for _, message := range file.Messages {
			model := generator.Model{
				Name:        message.Name,
				Type:        "struct",
				Description: fmt.Sprintf("消息: %s", message.Name),
				Fields:      []generator.Field{},
			}

			// 转换字段
			for _, field := range message.Fields {
				apiField := generator.Field{
					Name:        field.Name,
					Type:        s.convertProtobufType(field.Type),
					Required:    true, // protobuf字段默认必需
					Description: fmt.Sprintf("字段: %s", field.Name),
					Tags:        []string{"protobuf"},
				}

				model.Fields = append(model.Fields, apiField)
			}

			spec.Models = append(spec.Models, model)
		}
	}

	return spec
}

// convertMessageToModel 将protobuf消息转换为模型
func (s *CodeGenerationService) convertMessageToModel(messageName string, messages []*ast.ProtobufMessage) *generator.Model {
	for _, message := range messages {
		if message.Name == messageName {
			model := &generator.Model{
				Name:        message.Name,
				Type:        "struct",
				Description: fmt.Sprintf("消息: %s", message.Name),
				Fields:      []generator.Field{},
			}

			for _, field := range message.Fields {
				apiField := generator.Field{
					Name:        field.Name,
					Type:        s.convertProtobufType(field.Type),
					Required:    true,
					Description: fmt.Sprintf("字段: %s", field.Name),
					Tags:        []string{"protobuf"},
				}

				model.Fields = append(model.Fields, apiField)
			}

			return model
		}
	}

	return nil
}

// convertProtobufType 转换protobuf类型为Go类型
func (s *CodeGenerationService) convertProtobufType(protobufType string) string {
	switch protobufType {
	case "string":
		return "string"
	case "int32":
		return "int32"
	case "int64":
		return "int64"
	case "bool":
		return "bool"
	case "float":
		return "float32"
	case "double":
		return "float64"
	case "bytes":
		return "[]byte"
	default:
		// 如果是自定义类型，返回类型名
		return protobufType
	}
}

// generatePathFromRPC 从RPC方法生成路径
func (s *CodeGenerationService) generatePathFromRPC(rpc *ast.ProtobufRPC) string {
	// 将RPC方法名转换为kebab-case路径
	name := strings.ToLower(rpc.Name)
	name = strings.ReplaceAll(name, "create", "")
	name = strings.ReplaceAll(name, "get", "")
	name = strings.ReplaceAll(name, "update", "")
	name = strings.ReplaceAll(name, "delete", "")
	name = strings.ReplaceAll(name, "list", "")

	return "/" + name
}

// generateMethodFromRPC 从RPC方法生成HTTP方法
func (s *CodeGenerationService) generateMethodFromRPC(rpc *ast.ProtobufRPC) string {
	name := strings.ToLower(rpc.Name)
	if strings.HasPrefix(name, "create") || strings.HasPrefix(name, "add") {
		return "POST"
	} else if strings.HasPrefix(name, "get") || strings.HasPrefix(name, "list") || strings.HasPrefix(name, "find") {
		return "GET"
	} else if strings.HasPrefix(name, "update") || strings.HasPrefix(name, "modify") {
		return "PUT"
	} else if strings.HasPrefix(name, "delete") || strings.HasPrefix(name, "remove") {
		return "DELETE"
	}
	return "POST" // 默认使用POST
}

// generateHandlerName 生成处理器名称
func (s *CodeGenerationService) generateHandlerName(rpcName string) string {
	return strings.Title(rpcName) + "Handler"
}

// GenerateOptions 生成选项
type GenerateOptions struct {
	OutputDir     string
	ModuleName    string
	GenerateTypes []string
	Features      []string
	Verbose       bool
	DryRun        bool
}

// NewGenerateOptions 创建生成选项
func NewGenerateOptions() *GenerateOptions {
	return &GenerateOptions{
		GenerateTypes: []string{"api", "client", "tests", "docs"},
		Features:      []string{"validation", "swagger"},
		Verbose:       false,
		DryRun:        false,
	}
}

// SetOutputDir 设置输出目录
func (o *GenerateOptions) SetOutputDir(dir string) *GenerateOptions {
	o.OutputDir = dir
	return o
}

// SetModuleName 设置模块名
func (o *GenerateOptions) SetModuleName(name string) *GenerateOptions {
	o.ModuleName = name
	return o
}

// SetGenerateTypes 设置生成类型
func (o *GenerateOptions) SetGenerateTypes(types []string) *GenerateOptions {
	o.GenerateTypes = types
	return o
}

// SetFeatures 设置特性
func (o *GenerateOptions) SetFeatures(features []string) *GenerateOptions {
	o.Features = features
	return o
}

// SetVerbose 设置详细模式
func (o *GenerateOptions) SetVerbose(verbose bool) *GenerateOptions {
	o.Verbose = verbose
	return o
}

// SetDryRun 设置试运行模式
func (o *GenerateOptions) SetDryRun(dryRun bool) *GenerateOptions {
	o.DryRun = dryRun
	return o
}

// Validate 验证选项
func (o *GenerateOptions) Validate() error {
	if o.OutputDir == "" {
		return fmt.Errorf("输出目录不能为空")
	}

	if o.ModuleName == "" {
		return fmt.Errorf("模块名不能为空")
	}

	if len(o.GenerateTypes) == 0 {
		return fmt.Errorf("至少需要指定一种生成类型")
	}

	return nil
}

// GetAbsoluteOutputDir 获取绝对输出目录
func (o *GenerateOptions) GetAbsoluteOutputDir(projectPath string) string {
	if filepath.IsAbs(o.OutputDir) {
		return o.OutputDir
	}
	return filepath.Join(projectPath, o.OutputDir)
}
