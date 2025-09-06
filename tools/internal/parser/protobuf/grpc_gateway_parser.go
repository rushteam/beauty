package protobuf

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rushteam/beauty/tools/internal/parser/ast"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// GrpcGatewayParser 使用grpc-gateway官方包的完整解析器
type GrpcGatewayParser struct {
	workDir    string
	protocPath string
	offline    bool
}

// SetOffline 设置离线模式
func (p *GrpcGatewayParser) SetOffline(v bool) { p.offline = v }

// NewGrpcGatewayParser 创建新的grpc-gateway解析器
func NewGrpcGatewayParser(workDir string) *GrpcGatewayParser {
	return &GrpcGatewayParser{
		workDir:    workDir,
		protocPath: "protoc", // 默认使用系统PATH中的protoc
	}
}

// ParseFile 解析单个protobuf文件
func (p *GrpcGatewayParser) ParseFile(filename string) (*ast.ProtobufFile, error) {
	// 检查文件扩展名
	if !p.isProtobufFile(filename) {
		return nil, fmt.Errorf("文件 %s 不是protobuf文件", filename)
	}

	// 使用 buf + 官方描述符解析
	return p.parseWithBufDescriptors(filename)
}

// ParseDirectory 解析目录中的所有protobuf文件
func (p *GrpcGatewayParser) ParseDirectory(dir string) ([]*ast.ProtobufFile, error) {
	var files []*ast.ProtobufFile

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() && p.isProtobufFile(path) {
			file, err := p.ParseFile(path)
			if err != nil {
				return fmt.Errorf("解析文件 %s 失败: %w", path, err)
			}
			files = append(files, file)
		}

		return nil
	})

	return files, err
}

// parseWithBufDescriptors 使用 buf build 生成描述符并用官方反射API解析
func (p *GrpcGatewayParser) parseWithBufDescriptors(filename string) (*ast.ProtobufFile, error) {
	if err := p.ensureBufDependencies(); err != nil {
		return nil, fmt.Errorf("buf依赖未就绪: %w", err)
	}

	// 运行 buf build 输出 FileDescriptorSet 至 stdout
	cmd := exec.Command("buf", "build", "-o", "-")
	cmd.Dir = p.workDir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("buf build 失败: %s, stderr: %s", err, errb.String())
	}

	// 反序列化 FileDescriptorSet
	fds := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(out.Bytes(), fds); err != nil {
		return nil, fmt.Errorf("解析描述符集失败: %w", err)
	}

	// 构建 Files 注册表
	files, err := protodesc.NewFiles(&descriptorpb.FileDescriptorSet{File: fds.File})
	if err != nil {
		return nil, fmt.Errorf("构建文件注册表失败: %w", err)
	}

	// 查找目标文件（相对路径，统一分隔符）
	rel, err := filepath.Rel(p.workDir, filename)
	if err != nil {
		rel = filename
	}
	rel = filepath.ToSlash(rel)

	fd, err := files.FindFileByPath(rel)
	if err != nil {
		var found protoreflect.FileDescriptor
		files.RangeFiles(func(desc protoreflect.FileDescriptor) bool {
			if strings.HasSuffix(string(desc.Path()), rel) || strings.HasSuffix(rel, string(desc.Path())) {
				found = desc
				return false
			}
			return true
		})
		if found == nil {
			return nil, fmt.Errorf("未在描述符集中找到文件: %s", rel)
		}
		fd = found
	}

	// 构建 AST 结果
	result := &ast.ProtobufFile{
		Filename: filename,
		Package:  string(fd.Package()),
		Services: []*ast.ProtobufService{},
		Messages: []*ast.ProtobufMessage{},
		Imports:  []string{},
	}

	// 导入列表
	for i := 0; i < fd.Imports().Len(); i++ {
		imp := fd.Imports().Get(i)
		result.Imports = append(result.Imports, string(imp.Path()))
	}

	// go_package
	if opts, ok := fd.Options().(*descriptorpb.FileOptions); ok && opts != nil {
		if gp := opts.GetGoPackage(); gp != "" {
			result.GoPackage = gp
		}
	}

	// Services & Methods
	svcs := fd.Services()
	for si := 0; si < svcs.Len(); si++ {
		s := svcs.Get(si)
		sNode := &ast.ProtobufService{Name: string(s.Name()), RPCs: []*ast.ProtobufRPC{}}
		mths := s.Methods()
		for mi := 0; mi < mths.Len(); mi++ {
			m := mths.Get(mi)
			rpc := &ast.ProtobufRPC{
				Name:     string(m.Name()),
				Request:  string(m.Input().FullName()),
				Response: string(m.Output().FullName()),
			}
			// 解析 google.api.http 扩展
			if opts, ok := m.Options().(*descriptorpb.MethodOptions); ok && opts != nil {
				if ext := proto.GetExtension(opts, annotations.E_Http); ext != nil {
					if rule, ok := ext.(*annotations.HttpRule); ok && rule != nil {
						http := &ast.HTTPOptions{}
						switch pattern := rule.GetPattern().(type) {
						case *annotations.HttpRule_Get:
							http.Method = "GET"
							http.Path = pattern.Get
						case *annotations.HttpRule_Post:
							http.Method = "POST"
							http.Path = pattern.Post
						case *annotations.HttpRule_Put:
							http.Method = "PUT"
							http.Path = pattern.Put
						case *annotations.HttpRule_Delete:
							http.Method = "DELETE"
							http.Path = pattern.Delete
						case *annotations.HttpRule_Patch:
							http.Method = "PATCH"
							http.Path = pattern.Patch
						case *annotations.HttpRule_Custom:
							http.Method = strings.ToUpper(rule.GetCustom().GetKind())
							http.Path = rule.GetCustom().GetPath()
						}
						http.Body = rule.GetBody()
						http.ResponseBody = rule.GetResponseBody()
						// additional_bindings
						if len(rule.AdditionalBindings) > 0 {
							http.Additional = make([]*ast.HTTPOptions, 0, len(rule.AdditionalBindings))
							for _, ab := range rule.AdditionalBindings {
								add := &ast.HTTPOptions{}
								switch pat := ab.GetPattern().(type) {
								case *annotations.HttpRule_Get:
									add.Method = "GET"
									add.Path = pat.Get
								case *annotations.HttpRule_Post:
									add.Method = "POST"
									add.Path = pat.Post
								case *annotations.HttpRule_Put:
									add.Method = "PUT"
									add.Path = pat.Put
								case *annotations.HttpRule_Delete:
									add.Method = "DELETE"
									add.Path = pat.Delete
								case *annotations.HttpRule_Patch:
									add.Method = "PATCH"
									add.Path = pat.Patch
								case *annotations.HttpRule_Custom:
									add.Method = strings.ToUpper(ab.GetCustom().GetKind())
									add.Path = ab.GetCustom().GetPath()
								}
								add.Body = ab.GetBody()
								add.ResponseBody = ab.GetResponseBody()
								http.Additional = append(http.Additional, add)
							}
						}
						rpc.HTTPOptions = http
					}
				}
			}
			sNode.RPCs = append(sNode.RPCs, rpc)
		}
		result.Services = append(result.Services, sNode)
	}

	// Messages & Fields（当前文件内顶级消息）
	msgs := fd.Messages()
	for mi := 0; mi < msgs.Len(); mi++ {
		md := msgs.Get(mi)
		m := &ast.ProtobufMessage{Name: string(md.Name()), Fields: []*ast.ProtobufField{}}
		flds := md.Fields()
		for fi := 0; fi < flds.Len(); fi++ {
			f := flds.Get(fi)
			field := &ast.ProtobufField{
				Type: kindToString(f.Kind(), f),
				Name: string(f.Name()),
				Tag:  fmt.Sprintf("%d", f.Number()),
			}
			m.Fields = append(m.Fields, field)
		}
		result.Messages = append(result.Messages, m)
	}

	return result, nil
}

// kindToString 将字段Kind转为可读类型名
func kindToString(k protoreflect.Kind, f protoreflect.FieldDescriptor) string {
	switch k {
	case protoreflect.BoolKind:
		return "bool"
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return "int32"
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return "uint32"
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return "int64"
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return "uint64"
	case protoreflect.FloatKind:
		return "float"
	case protoreflect.DoubleKind:
		return "double"
	case protoreflect.StringKind:
		return "string"
	case protoreflect.BytesKind:
		return "bytes"
	case protoreflect.EnumKind:
		return string(f.Enum().FullName())
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return string(f.Message().FullName())
	default:
		return strings.ToLower(k.String())
	}
}

// parseWithSimpleMethod 使用简化的方法解析（临时实现）
func (p *GrpcGatewayParser) parseWithSimpleMethod(filename string) (*ast.ProtobufFile, error) {
	content, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(content), "\n")

	file := &ast.ProtobufFile{
		Filename: filename,
		Services: []*ast.ProtobufService{},
		Messages: []*ast.ProtobufMessage{},
		Imports:  []string{},
	}

	var currentService *ast.ProtobufService
	var currentMessage *ast.ProtobufMessage
	var inService bool
	var inMessage bool
	var serviceBraceLevel int
	var messageBraceLevel int

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}

		// 解析syntax
		if strings.HasPrefix(line, "syntax") {
			file.Syntax = p.extractValue(line, "syntax")
			continue
		}

		// 解析package
		if strings.HasPrefix(line, "package") {
			file.Package = p.extractValue(line, "package")
			continue
		}

		// 解析import
		if strings.HasPrefix(line, "import") {
			importPath := p.extractQuotedValue(line)
			if importPath != "" {
				file.Imports = append(file.Imports, importPath)
			}
			continue
		}

		// 解析option go_package
		if strings.HasPrefix(line, "option go_package") {
			file.GoPackage = p.extractQuotedValue(line)
			continue
		}

		// 解析service
		if strings.HasPrefix(line, "service ") {
			serviceName := p.extractValue(line, "service")
			currentService = &ast.ProtobufService{
				Name: serviceName,
				RPCs: []*ast.ProtobufRPC{},
			}
			file.Services = append(file.Services, currentService)
			inService = true
			serviceBraceLevel = 0
			continue
		}

		// 解析message
		if strings.HasPrefix(line, "message ") {
			messageName := p.extractValue(line, "message")
			currentMessage = &ast.ProtobufMessage{
				Name:   messageName,
				Fields: []*ast.ProtobufField{},
			}
			file.Messages = append(file.Messages, currentMessage)
			inMessage = true
			messageBraceLevel = 0
			continue
		}

		// 解析RPC方法
		if inService && strings.HasPrefix(line, "rpc ") {
			rpc := p.parseRPCWithHTTPOptions(line, i+1, lines, i)
			if rpc != nil {
				currentService.RPCs = append(currentService.RPCs, rpc)
			}
			continue
		}

		// 解析message字段
		if inMessage && !strings.HasPrefix(line, "}") {
			field := p.parseField(line)
			if field != nil {
				currentMessage.Fields = append(currentMessage.Fields, field)
			}
			continue
		}

		// 处理大括号
		if strings.Contains(line, "{") {
			if inService {
				serviceBraceLevel++
			}
			if inMessage {
				messageBraceLevel++
			}
		}
		if strings.Contains(line, "}") {
			if inService {
				serviceBraceLevel--
				if serviceBraceLevel == 0 {
					inService = false
					currentService = nil
				}
			}
			if inMessage {
				messageBraceLevel--
				if messageBraceLevel == 0 {
					inMessage = false
					currentMessage = nil
				}
			}
		}
	}

	return file, nil
}

// parseRPCWithHTTPOptions 解析RPC方法，包括HTTP选项
func (p *GrpcGatewayParser) parseRPCWithHTTPOptions(line string, lineNum int, allLines []string, startIndex int) *ast.ProtobufRPC {
	// 移除分号和大括号
	line = strings.TrimSuffix(line, ";")
	line = strings.TrimSuffix(line, "{")
	line = strings.TrimSpace(line)

	// 解析格式: rpc MethodName(RequestType) returns (ResponseType)
	parts := strings.Fields(line)
	if len(parts) < 5 {
		return nil
	}

	rpc := &ast.ProtobufRPC{
		Name:     parts[1],
		Request:  p.extractType(parts[2]),
		Response: p.extractType(parts[4]),
		LineNum:  lineNum,
	}

	// 解析HTTP选项
	httpOptions := p.parseHTTPOptions(allLines, startIndex)
	if httpOptions != nil {
		rpc.HTTPOptions = httpOptions
	}

	return rpc
}

// parseHTTPOptions 解析HTTP选项
func (p *GrpcGatewayParser) parseHTTPOptions(allLines []string, startIndex int) *ast.HTTPOptions {
	// 查找google.api.http选项
	for i := startIndex; i < len(allLines); i++ {
		line := strings.TrimSpace(allLines[i])

		// 如果遇到结束大括号，停止查找
		if line == "}" {
			break
		}

		// 查找HTTP选项
		if strings.Contains(line, "google.api.http") {
			return p.extractHTTPOptions(allLines, i)
		}
	}

	return nil
}

// extractHTTPOptions 提取HTTP选项
func (p *GrpcGatewayParser) extractHTTPOptions(allLines []string, startIndex int) *ast.HTTPOptions {
	options := &ast.HTTPOptions{}
	braceLevel := 0

	for i := startIndex; i < len(allLines); i++ {
		line := strings.TrimSpace(allLines[i])

		// 计算大括号层级
		if strings.Contains(line, "{") {
			braceLevel++
		}
		if strings.Contains(line, "}") {
			braceLevel--
			// 如果回到0，说明HTTP选项结束
			if braceLevel == 0 {
				break
			}
		}

		// 解析HTTP方法
		if strings.Contains(line, "get:") {
			options.Method = "GET"
			options.Path = p.extractQuotedValue(line)
		} else if strings.Contains(line, "post:") {
			options.Method = "POST"
			options.Path = p.extractQuotedValue(line)
		} else if strings.Contains(line, "put:") {
			options.Method = "PUT"
			options.Path = p.extractQuotedValue(line)
		} else if strings.Contains(line, "delete:") {
			options.Method = "DELETE"
			options.Path = p.extractQuotedValue(line)
		} else if strings.Contains(line, "patch:") {
			options.Method = "PATCH"
			options.Path = p.extractQuotedValue(line)
		}

		// 解析body选项
		if strings.Contains(line, "body:") {
			options.Body = p.extractQuotedValue(line)
		}
	}

	return options
}

// extractValue 提取值
func (p *GrpcGatewayParser) extractValue(line, key string) string {
	parts := strings.Fields(line)
	if len(parts) >= 2 {
		return strings.Trim(parts[1], `";`)
	}
	return ""
}

// extractQuotedValue 提取引号中的值
func (p *GrpcGatewayParser) extractQuotedValue(line string) string {
	start := strings.Index(line, `"`)
	if start == -1 {
		return ""
	}
	end := strings.Index(line[start+1:], `"`)
	if end == -1 {
		return ""
	}
	return line[start+1 : start+1+end]
}

// extractType 从括号中提取类型
func (p *GrpcGatewayParser) extractType(s string) string {
	s = strings.Trim(s, "()")
	return s
}

// parseField 解析message字段
func (p *GrpcGatewayParser) parseField(line string) *ast.ProtobufField {
	// 跳过注释和空行
	if strings.HasPrefix(line, "//") || strings.TrimSpace(line) == "" {
		return nil
	}

	// 跳过rpc、service等关键字
	if strings.HasPrefix(line, "rpc ") || strings.HasPrefix(line, "service ") {
		return nil
	}

	// 移除分号
	line = strings.TrimSuffix(line, ";")
	parts := strings.Fields(line)
	if len(parts) < 3 {
		return nil
	}

	// 解析格式: type name = tag
	fieldType := parts[0]
	fieldName := parts[1]
	fieldTag := ""

	// 查找等号后的标签
	for i, part := range parts {
		if part == "=" && i+1 < len(parts) {
			fieldTag = parts[i+1]
			break
		}
	}

	field := &ast.ProtobufField{
		Type: fieldType,
		Name: fieldName,
		Tag:  fieldTag,
	}

	return field
}

// isProtobufFile 检查文件是否为protobuf文件
func (p *GrpcGatewayParser) isProtobufFile(filename string) bool {
	return strings.HasSuffix(filename, ".proto")
}

// GenerateCode 生成代码
func (p *GrpcGatewayParser) GenerateCode(outputDir string, openapi bool) error {
	// 创建输出目录
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}

	// 确保buf依赖
	if err := p.ensureBufDependencies(); err != nil {
		return fmt.Errorf("安装buf依赖失败: %w", err)
	}

	// 动态生成 remote 插件模板，输出到指定目录
	openapiOut := filepath.Join(outputDir, "openapiv2")
	autogen := "version: v2\nplugins:\n" +
		"  - remote: buf.build/protocolbuffers/go:v1.36.2\n" +
		"    out: " + filepath.ToSlash(outputDir) + "\n" +
		"    opt:\n" +
		"      - paths=source_relative\n" +
		"  - remote: buf.build/grpc/go:v1.5.1\n" +
		"    out: " + filepath.ToSlash(outputDir) + "\n" +
		"    opt:\n" +
		"      - paths=source_relative\n" +
		"  - remote: buf.build/grpc-ecosystem/gateway:v2.22.0\n" +
		"    out: " + filepath.ToSlash(outputDir) + "\n" +
		"    opt:\n" +
		"      - paths=source_relative\n" +
		"      - generate_unbound_methods=true\n"
	if openapi {
		if err := os.MkdirAll(openapiOut, 0755); err != nil {
			return fmt.Errorf("创建openapi输出目录失败: %w", err)
		}
		autogen += "  - remote: buf.build/grpc-ecosystem/openapiv2:v2.22.0\n" +
			"    out: " + filepath.ToSlash(openapiOut) + "\n"
	}

	tmplPath := filepath.Join(p.workDir, "buf.gen.autogen.yaml")
	if err := os.WriteFile(tmplPath, []byte(autogen), 0644); err != nil {
		return fmt.Errorf("写入临时模板失败: %w", err)
	}

	// 运行 buf generate 使用临时模板
	cmd := exec.Command("buf", "generate", "--template", filepath.Base(tmplPath))
	cmd.Dir = p.workDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("buf generate失败: %s, stderr: %s", err, stderr.String())
	}

	return nil
}

// ensureBufDependencies 确保buf依赖已安装
func (p *GrpcGatewayParser) ensureBufDependencies() error {
	// 检查buf.yaml是否存在
	bufYamlPath := filepath.Join(p.workDir, "buf.yaml")
	if _, err := os.Stat(bufYamlPath); os.IsNotExist(err) {
		// 创建buf.yaml
		if err := p.createBufYaml(); err != nil {
			return fmt.Errorf("创建buf.yaml失败: %w", err)
		}
	}

	// 检查buf.gen.yaml是否存在
	bufGenYamlPath := filepath.Join(p.workDir, "buf.gen.yaml")
	if _, err := os.Stat(bufGenYamlPath); os.IsNotExist(err) {
		// 创建buf.gen.yaml
		if err := p.createBufGenYaml(); err != nil {
			return fmt.Errorf("创建buf.gen.yaml失败: %w", err)
		}
	}

	if p.offline {
		return nil
	}

	// 运行buf dep update来安装依赖
	cmd := exec.Command("buf", "dep", "update")
	cmd.Dir = p.workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("buf dep update失败: %s, stderr: %s", err, stderr.String())
	}

	return nil
}

// createBufYaml 创建buf.yaml文件
func (p *GrpcGatewayParser) createBufYaml() error {
	bufYamlContent := `version: v2
name: buf.build/example/myrepo
deps:
  - buf.build/googleapis/googleapis
  - buf.build/grpc-ecosystem/grpc-gateway

lint:
  use:
    - DEFAULT
breaking:
  use:
    - FILE
`

	bufYamlPath := filepath.Join(p.workDir, "buf.yaml")
	return os.WriteFile(bufYamlPath, []byte(bufYamlContent), 0644)
}

// createBufGenYaml 创建buf.gen.yaml文件
func (p *GrpcGatewayParser) createBufGenYaml() error {
	bufGenYamlContent := `version: v2
plugins:
  - remote: buf.build/protocolbuffers/go:v1.36.2
    out: gen/go
    opt:
      - paths=source_relative
  - remote: buf.build/grpc/go:v1.5.1
    out: gen/go
    opt:
      - paths=source_relative
  - remote: buf.build/grpc-ecosystem/gateway:v2.22.0
    out: gen/go
    opt:
      - paths=source_relative
      - generate_unbound_methods=true
`

	bufGenYamlPath := filepath.Join(p.workDir, "buf.gen.yaml")
	return os.WriteFile(bufGenYamlPath, []byte(bufGenYamlContent), 0644)
}

// generateGoCode 生成Go代码
func (p *GrpcGatewayParser) generateGoCode(filename string, outputDir string) error {
	// 使用buf generate来生成代码
	cmd := exec.Command("buf", "generate", "--template", "buf.gen.yaml")
	cmd.Dir = p.workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("buf generate失败: %s, stderr: %s", err, stderr.String())
	}

	return nil
}

// generateGrpcGatewayCode 生成gRPC-Gateway代码
func (p *GrpcGatewayParser) generateGrpcGatewayCode(filename string, outputDir string) error {
	// 使用buf generate来生成代码（包含在generateGoCode中）
	return nil
}
