// Package doctor 实现 `beauty doctor`：检查本地开发环境与当前项目的常见问题。
package doctor

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/urfave/cli/v3"
)

// MinGoVersion 是 beauty 核心要求的最低 Go 版本。
const MinGoVersion = "1.26"

const (
	coreModule    = "github.com/rushteam/beauty"
	contribPrefix = coreModule + "/contrib/"
)

// Level 检查结果等级。
type Level int

const (
	OK Level = iota
	Warn
	Fail
)

func (l Level) icon() string {
	switch l {
	case OK:
		return "✅"
	case Warn:
		return "⚠️ "
	default:
		return "❌"
	}
}

// Result 单项检查结果。
type Result struct {
	Name   string
	Level  Level
	Detail string
	Hint   string
}

// Command 返回 `beauty doctor` 子命令。
func Command() *cli.Command {
	return &cli.Command{
		Name:  "doctor",
		Usage: "🩺 检查开发环境与项目配置",
		Description: `检查本地开发环境与当前项目：
   • Go 版本是否满足要求(>= ` + MinGoVersion + `)
   • go.mod、beauty 依赖版本、遗留的 replace 指令
   • 已引入的 contrib 模块
   • protobuf 工具链(buf / protoc-gen-go / protoc-gen-go-grpc / protoc-gen-connect-go)
   • Docker、配置文件、gofmt
   • 在 beauty 仓库本身运行时，额外检查核心是否误引入 contrib`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "path",
				Aliases: []string{"p"},
				Usage:   "项目目录",
				Value:   ".",
			},
			&cli.BoolFlag{
				Name:  "strict",
				Usage: "有警告时也以非 0 退出(适合 CI)",
			},
		},
		Action: action,
	}
}

func action(ctx context.Context, c *cli.Command) error {
	root, err := filepath.Abs(c.String("path"))
	if err != nil {
		return cli.Exit(fmt.Sprintf("❌ 解析路径失败: %v", err), 1)
	}

	fmt.Printf("🩺 beauty doctor: %s\n\n", root)
	results := Run(ctx, root)

	var warns, fails int
	for _, r := range results {
		fmt.Printf("%s %s", r.Level.icon(), r.Name)
		if r.Detail != "" {
			fmt.Printf(": %s", r.Detail)
		}
		fmt.Println()
		if r.Hint != "" && r.Level != OK {
			fmt.Printf("     💡 %s\n", r.Hint)
		}
		switch r.Level {
		case Warn:
			warns++
		case Fail:
			fails++
		}
	}

	fmt.Printf("\n共 %d 项：%d 通过，%d 警告，%d 失败\n", len(results), len(results)-warns-fails, warns, fails)
	if fails > 0 || (c.Bool("strict") && warns > 0) {
		return cli.Exit("", 1)
	}
	return nil
}

// Run 执行全部检查。
func Run(ctx context.Context, root string) []Result {
	var results []Result
	results = append(results, checkGo(ctx))

	mod, err := ReadGoMod(filepath.Join(root, "go.mod"))
	if err != nil {
		results = append(results, Result{
			Name:   "go.mod",
			Level:  Fail,
			Detail: "未找到或无法解析",
			Hint:   "请在项目根目录运行，或用 --path 指定；新项目可用 `beauty new <name>` 创建",
		})
	} else {
		results = append(results, checkGoMod(mod)...)
		if mod.Module == coreModule {
			results = append(results, checkNoContribPollution(ctx, root))
		}
	}

	results = append(results, checkTools()...)
	results = append(results, checkConfig(root), checkGofmt(ctx, root))
	return results
}

func checkGo(ctx context.Context) Result {
	out, err := exec.CommandContext(ctx, "go", "env", "GOVERSION").Output()
	if err != nil {
		return Result{Name: "Go", Level: Fail, Detail: "未找到 go 命令", Hint: "安装 Go: https://go.dev/dl/"}
	}
	v := strings.TrimSpace(string(out))
	if CompareVersion(strings.TrimPrefix(v, "go"), MinGoVersion) < 0 {
		return Result{Name: "Go", Level: Fail, Detail: v, Hint: "beauty 需要 Go >= " + MinGoVersion}
	}
	return Result{Name: "Go", Level: OK, Detail: v}
}

// GoMod 是 go.mod 中 doctor 关心的字段。
type GoMod struct {
	Module   string
	Go       string
	Requires map[string]string // path -> version
	Replaces map[string]string // path -> 替换目标
}

// ReadGoMod 读取并解析 go.mod 文件。
func ReadGoMod(path string) (*GoMod, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseGoMod(data), nil
}

// ParseGoMod 以行为单位解析 go.mod，只识别 module/go/require/replace，足够 doctor 使用。
func ParseGoMod(data []byte) *GoMod {
	m := &GoMod{Requires: map[string]string{}, Replaces: map[string]string{}}
	var block string
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if block != "" {
			if line == ")" {
				block = ""
				continue
			}
			m.addDirective(block, line)
			continue
		}
		key, rest, _ := strings.Cut(line, " ")
		rest = strings.TrimSpace(rest)
		switch key {
		case "module":
			m.Module = strings.Trim(rest, `"`)
		case "go":
			m.Go = rest
		case "require", "replace":
			if rest == "(" {
				block = key
				continue
			}
			m.addDirective(key, rest)
		}
	}
	return m
}

func (m *GoMod) addDirective(kind, line string) {
	switch kind {
	case "require":
		f := strings.Fields(line)
		if len(f) >= 2 {
			m.Requires[f[0]] = f[1]
		}
	case "replace":
		from, to, ok := strings.Cut(line, "=>")
		if !ok {
			return
		}
		f := strings.Fields(from)
		if len(f) == 0 {
			return
		}
		m.Replaces[f[0]] = strings.TrimSpace(to)
	}
}

// ContribDeps 返回 go.mod 中引入的 contrib 模块名(如 "gorm"、"codec/kitex")。
func (m *GoMod) ContribDeps() []string {
	var names []string
	for p := range m.Requires {
		if name, ok := strings.CutPrefix(p, contribPrefix); ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

func checkGoMod(m *GoMod) []Result {
	results := []Result{{Name: "go.mod", Level: OK, Detail: "module " + m.Module}}

	if m.Go != "" && CompareVersion(m.Go, MinGoVersion) < 0 {
		results = append(results, Result{
			Name:   "go 指令",
			Level:  Warn,
			Detail: "go " + m.Go,
			Hint:   "beauty 需要 go >= " + MinGoVersion + "，执行 `go mod edit -go=" + MinGoVersion + "`",
		})
	}

	if m.Module != coreModule && !strings.HasPrefix(m.Module, coreModule+"/") {
		if v, ok := m.Requires[coreModule]; ok {
			results = append(results, Result{Name: "beauty 依赖", Level: OK, Detail: v})
		} else {
			results = append(results, Result{
				Name:   "beauty 依赖",
				Level:  Warn,
				Detail: "go.mod 未 require " + coreModule,
				Hint:   "执行 `go get " + coreModule + "@latest`",
			})
		}
	}

	var local []string
	for p, to := range m.Replaces {
		if (p == coreModule || strings.HasPrefix(p, contribPrefix)) && isLocalPath(to) {
			local = append(local, p+" => "+to)
		}
	}
	slices.Sort(local)
	if len(local) > 0 {
		results = append(results, Result{
			Name:   "replace",
			Level:  Warn,
			Detail: strings.Join(local, "; "),
			Hint:   "本地联调用的 replace 指向本地路径，发布/部署前记得去掉",
		})
	}

	if deps := m.ContribDeps(); len(deps) > 0 {
		results = append(results, Result{Name: "contrib 模块", Level: OK, Detail: strings.Join(deps, ", ")})
	}
	return results
}

func isLocalPath(p string) bool {
	p = strings.TrimSpace(p)
	return strings.HasPrefix(p, ".") || strings.HasPrefix(p, "/")
}

func checkNoContribPollution(ctx context.Context, root string) Result {
	cmd := exec.CommandContext(ctx, "go", "list", "-deps", "./...")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return Result{Name: "核心无 contrib 依赖", Level: Warn, Detail: "go list 执行失败: " + err.Error()}
	}
	var bad []string
	for line := range strings.SplitSeq(string(out), "\n") {
		if strings.HasPrefix(line, contribPrefix) {
			bad = append(bad, line)
		}
	}
	if len(bad) > 0 {
		return Result{
			Name:   "核心无 contrib 依赖",
			Level:  Fail,
			Detail: strings.Join(bad, ", "),
			Hint:   "核心模块不得 import contrib/，请把相关代码移到 contrib 或改为依赖核心接口",
		}
	}
	return Result{Name: "核心无 contrib 依赖", Level: OK}
}

type tool struct {
	bin      string
	purpose  string
	install  string
	required bool
}

var tools = []tool{
	{"buf", "protobuf 编译/lint", "https://buf.build/docs/installation", false},
	{"protoc-gen-go", "生成 protobuf Go 代码", "go install google.golang.org/protobuf/cmd/protoc-gen-go@latest", false},
	{"protoc-gen-go-grpc", "生成 gRPC 代码", "go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest", false},
	{"protoc-gen-connect-go", "生成 Connect 代码(beauty add connect)", "go install connectrpc.com/connect/cmd/protoc-gen-connect-go@latest", false},
	{"docker", "运行依赖服务/可观测性栈", "https://docs.docker.com/get-docker/", false},
}

func checkTools() []Result {
	results := make([]Result, 0, len(tools))
	for _, t := range tools {
		path, err := exec.LookPath(t.bin)
		if err != nil {
			level := Warn
			if t.required {
				level = Fail
			}
			results = append(results, Result{Name: t.bin, Level: level, Detail: "未安装(" + t.purpose + ")", Hint: t.install})
			continue
		}
		results = append(results, Result{Name: t.bin, Level: OK, Detail: path})
	}
	return results
}

func checkConfig(root string) Result {
	for _, p := range []string{"config/dev/app.yaml", "config/app.yaml", "config.yaml"} {
		if _, err := os.Stat(filepath.Join(root, p)); err == nil {
			return Result{Name: "配置文件", Level: OK, Detail: p}
		}
	}
	return Result{
		Name:   "配置文件",
		Level:  Warn,
		Detail: "未找到 config/dev/app.yaml",
		Hint:   "`beauty dev` 默认读取 config/dev/app.yaml，可用 --config 指定",
	}
}

func checkGofmt(ctx context.Context, root string) Result {
	if _, err := exec.LookPath("gofmt"); err != nil {
		return Result{Name: "gofmt", Level: Warn, Detail: "未找到 gofmt"}
	}
	cmd := exec.CommandContext(ctx, "gofmt", "-l", ".")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return Result{Name: "gofmt", Level: Warn, Detail: "执行失败: " + err.Error()}
	}
	files := strings.Fields(string(out))
	if len(files) == 0 {
		return Result{Name: "gofmt", Level: OK}
	}
	detail := strings.Join(files, ", ")
	if len(files) > 5 {
		detail = strings.Join(files[:5], ", ") + fmt.Sprintf(" 等 %d 个文件", len(files))
	}
	return Result{Name: "gofmt", Level: Warn, Detail: detail, Hint: "执行 `gofmt -w .`"}
}

// CompareVersion 比较形如 "1.26"、"1.26.2"、"v1.2.3" 的版本号，忽略预发布后缀。
// a < b 返回 -1，相等返回 0，a > b 返回 1。
func CompareVersion(a, b string) int {
	pa, pb := versionParts(a), versionParts(b)
	for i := range max(len(pa), len(pb)) {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
	}
	return 0
}

func versionParts(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+ "); i >= 0 {
		v = v[:i]
	}
	var parts []int
	for s := range strings.SplitSeq(v, ".") {
		end := 0
		for end < len(s) && s[end] >= '0' && s[end] <= '9' {
			end++
		}
		n, _ := strconv.Atoi(s[:end])
		parts = append(parts, n)
		if end < len(s) {
			break
		}
	}
	return parts
}
