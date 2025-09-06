package protobuf

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rushteam/beauty/tools/internal/parser/ast"
)

// Parser 用于解析protobuf文件
type Parser struct {
	// 支持的文件扩展名
	extensions []string
	// 工作目录
	workDir string
}

// NewParser 创建新的protobuf解析器
func NewParser(workDir string) *Parser {
	return &Parser{
		extensions: []string{".proto"},
		workDir:    workDir,
	}
}

// ParseFile 解析单个protobuf文件
func (p *Parser) ParseFile(filename string) (*ast.ProtobufFile, error) {
	// 检查文件扩展名
	if !p.isProtobufFile(filename) {
		return nil, fmt.Errorf("文件 %s 不是protobuf文件", filename)
	}

	// 读取文件内容
	content, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("读取文件失败: %w", err)
	}

	// 解析protobuf内容
	return p.parseContent(string(content), filename)
}

// ParseDirectory 解析目录中的所有protobuf文件
func (p *Parser) ParseDirectory(dir string) ([]*ast.ProtobufFile, error) {
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

// parseContent 解析protobuf内容
func (p *Parser) parseContent(content, filename string) (*ast.ProtobufFile, error) {
	lines := strings.Split(content, "\n")

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
			file.Syntax = p.extractSyntax(line)
			continue
		}

		// 解析package
		if strings.HasPrefix(line, "package") {
			file.Package = p.extractPackage(line)
			continue
		}

		// 解析import
		if strings.HasPrefix(line, "import") {
			importPath := p.extractImport(line)
			if importPath != "" {
				file.Imports = append(file.Imports, importPath)
			}
			continue
		}

		// 解析option go_package
		if strings.HasPrefix(line, "option go_package") {
			file.GoPackage = p.extractGoPackage(line)
			continue
		}

		// 解析service
		if strings.HasPrefix(line, "service ") {
			serviceName := p.extractServiceName(line)
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
			messageName := p.extractMessageName(line)
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
			rpc := p.parseRPC(line, i+1)
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
				// 只有当serviceBraceLevel回到0时才退出service解析
				if serviceBraceLevel == 0 {
					inService = false
					currentService = nil
				}
			}
			if inMessage {
				messageBraceLevel--
				// 只有当messageBraceLevel回到0时才退出message解析
				if messageBraceLevel == 0 {
					inMessage = false
					currentMessage = nil
				}
			}
		}
	}

	return file, nil
}

// isProtobufFile 检查文件是否为protobuf文件
func (p *Parser) isProtobufFile(filename string) bool {
	ext := filepath.Ext(filename)
	for _, e := range p.extensions {
		if ext == e {
			return true
		}
	}
	return false
}

// extractSyntax 提取syntax信息
func (p *Parser) extractSyntax(line string) string {
	parts := strings.Fields(line)
	if len(parts) >= 2 {
		return strings.Trim(parts[1], `";`)
	}
	return "proto3"
}

// extractPackage 提取package信息
func (p *Parser) extractPackage(line string) string {
	parts := strings.Fields(line)
	if len(parts) >= 2 {
		return strings.Trim(parts[1], `;`)
	}
	return ""
}

// extractImport 提取import信息
func (p *Parser) extractImport(line string) string {
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

// extractGoPackage 提取go_package信息
func (p *Parser) extractGoPackage(line string) string {
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

// extractServiceName 提取service名称
func (p *Parser) extractServiceName(line string) string {
	parts := strings.Fields(line)
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

// extractMessageName 提取message名称
func (p *Parser) extractMessageName(line string) string {
	parts := strings.Fields(line)
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

// parseRPC 解析RPC方法
func (p *Parser) parseRPC(line string, lineNum int) *ast.ProtobufRPC {
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

	// 检查是否有HTTP注解
	// 这里可以扩展解析google.api.http注解
	return rpc
}

// parseField 解析message字段
func (p *Parser) parseField(line string) *ast.ProtobufField {
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

// extractType 从括号中提取类型
func (p *Parser) extractType(s string) string {
	s = strings.Trim(s, "()")
	return s
}
