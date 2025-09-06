package buf

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rushteam/beauty/tools/internal/parser/ast"
)

// Manager 管理buf操作
type Manager struct {
	workDir string
	config  *Config
}

// Config buf配置
type Config struct {
	Version  string   `yaml:"version"`
	Name     string   `yaml:"name"`
	Deps     []string `yaml:"deps"`
	Build    Build    `yaml:"build"`
	Lint     Lint     `yaml:"lint"`
	Breaking Breaking `yaml:"breaking"`
}

// Build 构建配置
type Build struct {
	Roots []string `yaml:"roots"`
}

// Lint lint配置
type Lint struct {
	Use []string `yaml:"use"`
}

// Breaking 破坏性变更检测配置
type Breaking struct {
	Use []string `yaml:"use"`
}

// GenConfig 代码生成配置
type GenConfig struct {
	Version string   `yaml:"version"`
	Managed Managed  `yaml:"managed"`
	Plugins []Plugin `yaml:"plugins"`
}

// Managed 管理配置
type Managed struct {
	Enabled bool `yaml:"enabled"`
}

// Plugin 插件配置
type Plugin struct {
	Name string `yaml:"name"`
	Out  string `yaml:"out"`
	Opt  string `yaml:"opt"`
}

// NewManager 创建新的buf管理器
func NewManager(workDir string) *Manager {
	return &Manager{
		workDir: workDir,
		config: &Config{
			Version: "v2",
			Name:    "buf.build/example/myrepo",
			Deps: []string{
				"buf.build/googleapis/googleapis",
			},
			Build: Build{
				Roots: []string{"api"},
			},
			Lint: Lint{
				Use: []string{"DEFAULT"},
			},
			Breaking: Breaking{
				Use: []string{"FILE"},
			},
		},
	}
}

// Init 初始化buf模块
func (m *Manager) Init() error {
	// 检查buf是否已安装
	if !m.isBufInstalled() {
		return fmt.Errorf("buf未安装，请先安装buf工具")
	}

	// 创建buf.yaml配置文件
	if err := m.createBufYaml(); err != nil {
		return fmt.Errorf("创建buf.yaml失败: %w", err)
	}

	// 创建buf.gen.yaml配置文件
	if err := m.createBufGenYaml(); err != nil {
		return fmt.Errorf("创建buf.gen.yaml失败: %w", err)
	}

	// 初始化buf模块（如果不存在）
	if err := m.runBufCommand("mod", "init"); err != nil {
		// 如果模块已存在，忽略错误
		if !strings.Contains(err.Error(), "already exists") {
			return fmt.Errorf("初始化buf模块失败: %w", err)
		}
	}

	// 更新依赖
	if err := m.runBufCommand("mod", "update"); err != nil {
		return fmt.Errorf("更新buf依赖失败: %w", err)
	}

	return nil
}

// Generate 生成代码
func (m *Manager) Generate() error {
	return m.runBufCommand("generate")
}

// Lint 检查protobuf文件
func (m *Manager) Lint() error {
	return m.runBufCommand("lint")
}

// Format 格式化protobuf文件
func (m *Manager) Format() error {
	return m.runBufCommand("format", "-w")
}

// Breaking 检查破坏性变更
func (m *Manager) Breaking() error {
	return m.runBufCommand("breaking", "--against", ".git#branch=main")
}

// ParseProtobufFiles 解析protobuf文件
func (m *Manager) ParseProtobufFiles() ([]*ast.ProtobufFile, error) {
	var files []*ast.ProtobufFile

	// 查找所有.proto文件
	protoFiles, err := m.findProtoFiles()
	if err != nil {
		return nil, fmt.Errorf("查找protobuf文件失败: %w", err)
	}

	// 解析每个文件
	for _, file := range protoFiles {
		content, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("读取文件 %s 失败: %w", file, err)
		}

		// 使用buf解析protobuf文件
		protobufFile, err := m.parseWithBuf(string(content), file)
		if err != nil {
			return nil, fmt.Errorf("解析文件 %s 失败: %w", file, err)
		}

		files = append(files, protobufFile)
	}

	return files, nil
}

// isBufInstalled 检查buf是否已安装
func (m *Manager) isBufInstalled() bool {
	_, err := exec.LookPath("buf")
	return err == nil
}

// createBufYaml 创建buf.yaml配置文件
func (m *Manager) createBufYaml() error {
	configPath := filepath.Join(m.workDir, "buf.yaml")

	// 检查文件是否已存在
	if _, err := os.Stat(configPath); err == nil {
		return nil // 文件已存在，跳过创建
	}

	// 创建配置文件内容
	content := fmt.Sprintf(`version: %s
name: %s
deps:
%s
build:
  roots:
%s
lint:
  use:
%s
breaking:
  use:
%s`,
		m.config.Version,
		m.config.Name,
		m.formatDeps(),
		m.formatRoots(),
		m.formatLint(),
		m.formatBreaking())

	return os.WriteFile(configPath, []byte(content), 0644)
}

// createBufGenYaml 创建buf.gen.yaml配置文件
func (m *Manager) createBufGenYaml() error {
	configPath := filepath.Join(m.workDir, "buf.gen.yaml")

	// 检查文件是否已存在
	if _, err := os.Stat(configPath); err == nil {
		return nil // 文件已存在，跳过创建
	}

	// 创建代码生成配置
	genConfig := &GenConfig{
		Version: "v2",
		Managed: Managed{
			Enabled: true,
		},
		Plugins: []Plugin{
			{
				Name: "go",
				Out:  "api/v1",
				Opt:  "paths=source_relative",
			},
			{
				Name: "go-grpc",
				Out:  "api/v1",
				Opt:  "paths=source_relative",
			},
			{
				Name: "grpc-gateway",
				Out:  "api/v1",
				Opt:  "paths=source_relative",
			},
		},
	}

	content := fmt.Sprintf(`version: %s
managed:
  enabled: %t
plugins:
%s`,
		genConfig.Version,
		genConfig.Managed.Enabled,
		m.formatPlugins(genConfig.Plugins))

	return os.WriteFile(configPath, []byte(content), 0644)
}

// runBufCommand 运行buf命令
func (m *Manager) runBufCommand(args ...string) error {
	cmd := exec.Command("buf", args...)
	cmd.Dir = m.workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("buf命令执行失败: %s, stderr: %s", err.Error(), stderr.String())
	}

	return nil
}

// findProtoFiles 查找所有.proto文件
func (m *Manager) findProtoFiles() ([]string, error) {
	var files []string

	err := filepath.Walk(m.workDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() && strings.HasSuffix(path, ".proto") {
			files = append(files, path)
		}

		return nil
	})

	return files, err
}

// parseWithBuf 使用buf解析protobuf文件
func (m *Manager) parseWithBuf(content, filename string) (*ast.ProtobufFile, error) {
	// 这里可以集成buf的解析能力
	// 目前使用简单的文本解析
	return m.simpleParse(content, filename)
}

// simpleParse 简单的protobuf解析
func (m *Manager) simpleParse(content, filename string) (*ast.ProtobufFile, error) {
	lines := strings.Split(content, "\n")

	file := &ast.ProtobufFile{
		Filename: filename,
		Services: []*ast.ProtobufService{},
		Messages: []*ast.ProtobufMessage{},
		Imports:  []string{},
	}

	var currentService *ast.ProtobufService
	var currentMessage *ast.ProtobufMessage
	var braceLevel int

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}

		// 解析syntax
		if strings.HasPrefix(line, "syntax") {
			file.Syntax = m.extractValue(line, "syntax")
			continue
		}

		// 解析package
		if strings.HasPrefix(line, "package") {
			file.Package = m.extractValue(line, "package")
			continue
		}

		// 解析import
		if strings.HasPrefix(line, "import") {
			importPath := m.extractQuotedValue(line)
			if importPath != "" {
				file.Imports = append(file.Imports, importPath)
			}
			continue
		}

		// 解析option go_package
		if strings.HasPrefix(line, "option go_package") {
			file.GoPackage = m.extractQuotedValue(line)
			continue
		}

		// 解析service
		if strings.HasPrefix(line, "service ") {
			serviceName := m.extractValue(line, "service")
			currentService = &ast.ProtobufService{
				Name: serviceName,
				RPCs: []*ast.ProtobufRPC{},
			}
			file.Services = append(file.Services, currentService)
			braceLevel = 0
			continue
		}

		// 解析message
		if strings.HasPrefix(line, "message ") {
			messageName := m.extractValue(line, "message")
			currentMessage = &ast.ProtobufMessage{
				Name:   messageName,
				Fields: []*ast.ProtobufField{},
			}
			file.Messages = append(file.Messages, currentMessage)
			braceLevel = 0
			continue
		}

		// 解析RPC方法
		if currentService != nil && strings.HasPrefix(line, "rpc ") {
			rpc := m.parseRPC(line, i+1)
			if rpc != nil {
				currentService.RPCs = append(currentService.RPCs, rpc)
			}
			continue
		}

		// 解析message字段
		if currentMessage != nil && !strings.HasPrefix(line, "}") {
			field := m.parseField(line)
			if field != nil {
				currentMessage.Fields = append(currentMessage.Fields, field)
			}
			continue
		}

		// 处理大括号
		if strings.Contains(line, "{") {
			braceLevel++
		}
		if strings.Contains(line, "}") {
			braceLevel--
			if braceLevel == 0 {
				currentService = nil
				currentMessage = nil
			}
		}
	}

	return file, nil
}

// extractValue 提取值
func (m *Manager) extractValue(line, key string) string {
	parts := strings.Fields(line)
	if len(parts) >= 2 {
		return strings.Trim(parts[1], `";`)
	}
	return ""
}

// extractQuotedValue 提取引号中的值
func (m *Manager) extractQuotedValue(line string) string {
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

// parseRPC 解析RPC方法
func (m *Manager) parseRPC(line string, lineNum int) *ast.ProtobufRPC {
	parts := strings.Fields(line)
	if len(parts) < 6 {
		return nil
	}

	rpc := &ast.ProtobufRPC{
		Name:     parts[1],
		Request:  m.extractType(parts[2]),
		Response: m.extractType(parts[5]),
		LineNum:  lineNum,
	}

	return rpc
}

// parseField 解析message字段
func (m *Manager) parseField(line string) *ast.ProtobufField {
	if strings.HasPrefix(line, "//") || strings.TrimSpace(line) == "" {
		return nil
	}

	parts := strings.Fields(line)
	if len(parts) < 3 {
		return nil
	}

	field := &ast.ProtobufField{
		Type: parts[0],
		Name: parts[1],
		Tag:  parts[2],
	}

	return field
}

// extractType 从括号中提取类型
func (m *Manager) extractType(s string) string {
	s = strings.Trim(s, "()")
	return s
}

// formatDeps 格式化依赖
func (m *Manager) formatDeps() string {
	var result strings.Builder
	for _, dep := range m.config.Deps {
		result.WriteString(fmt.Sprintf("  - %s\n", dep))
	}
	return result.String()
}

// formatRoots 格式化根目录
func (m *Manager) formatRoots() string {
	var result strings.Builder
	for _, root := range m.config.Build.Roots {
		result.WriteString(fmt.Sprintf("    - %s\n", root))
	}
	return result.String()
}

// formatLint 格式化lint配置
func (m *Manager) formatLint() string {
	var result strings.Builder
	for _, rule := range m.config.Lint.Use {
		result.WriteString(fmt.Sprintf("    - %s\n", rule))
	}
	return result.String()
}

// formatBreaking 格式化breaking配置
func (m *Manager) formatBreaking() string {
	var result strings.Builder
	for _, rule := range m.config.Breaking.Use {
		result.WriteString(fmt.Sprintf("    - %s\n", rule))
	}
	return result.String()
}

// formatPlugins 格式化插件配置
func (m *Manager) formatPlugins(plugins []Plugin) string {
	var result strings.Builder
	for _, plugin := range plugins {
		result.WriteString(fmt.Sprintf(`  - name: %s
    out: %s
    opt: %s
`, plugin.Name, plugin.Out, plugin.Opt))
	}
	return result.String()
}
