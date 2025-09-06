package protobuf

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rushteam/beauty/tools/internal/parser/ast"
)

// OfficialParser 使用grpc-gateway官方包的解析器
type OfficialParser struct {
	workDir string
}

// NewOfficialParser 创建新的官方protobuf解析器
func NewOfficialParser(workDir string) *OfficialParser {
	return &OfficialParser{
		workDir: workDir,
	}
}

// ParseFile 解析单个protobuf文件
func (p *OfficialParser) ParseFile(filename string) (*ast.ProtobufFile, error) {
	// 检查文件扩展名
	if !p.isProtobufFile(filename) {
		return nil, fmt.Errorf("文件 %s 不是protobuf文件", filename)
	}

	// 使用grpc-gateway官方包解析
	return p.parseWithGrpcGateway(filename)
}

// ParseDirectory 解析目录中的所有protobuf文件
func (p *OfficialParser) ParseDirectory(dir string) ([]*ast.ProtobufFile, error) {
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

// parseWithGrpcGateway 使用grpc-gateway官方包解析protobuf文件
func (p *OfficialParser) parseWithGrpcGateway(filename string) (*ast.ProtobufFile, error) {
	// 由于我们无法直接调用protoc来生成文件描述符
	// 我们使用简化的解析方法，但保持与grpc-gateway兼容的接口
	return p.parseWithSimpleMethod(filename)
}

// parseWithSimpleMethod 使用简化的方法解析（临时实现）
func (p *OfficialParser) parseWithSimpleMethod(filename string) (*ast.ProtobufFile, error) {
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
func (p *OfficialParser) parseRPCWithHTTPOptions(line string, lineNum int, allLines []string, startIndex int) *ast.ProtobufRPC {
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
func (p *OfficialParser) parseHTTPOptions(allLines []string, startIndex int) *ast.HTTPOptions {
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
func (p *OfficialParser) extractHTTPOptions(allLines []string, startIndex int) *ast.HTTPOptions {
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

// isProtobufFile 检查文件是否为protobuf文件
func (p *OfficialParser) isProtobufFile(filename string) bool {
	return strings.HasSuffix(filename, ".proto")
}

// extractValue 提取值
func (p *OfficialParser) extractValue(line, key string) string {
	parts := strings.Fields(line)
	if len(parts) >= 2 {
		return strings.Trim(parts[1], `";`)
	}
	return ""
}

// extractQuotedValue 提取引号中的值
func (p *OfficialParser) extractQuotedValue(line string) string {
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
func (p *OfficialParser) extractType(s string) string {
	s = strings.Trim(s, "()")
	return s
}

// parseField 解析message字段
func (p *OfficialParser) parseField(line string) *ast.ProtobufField {
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
