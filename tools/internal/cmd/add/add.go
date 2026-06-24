package add

import (
	"context"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"
)

// Command 返回 `beauty add` 子命令，用于向现有项目增量添加端点/任务骨架。
func Command() *cli.Command {
	return &cli.Command{
		Name:  "add",
		Usage: "➕ 向现有项目增量添加代码骨架(handler/job)",
		Description: `在现有 Beauty 项目中快速生成新的代码骨架：
   • beauty add handler Order   # 生成 HTTP handler 骨架
   • beauty add job Cleanup     # 生成定时任务骨架
生成的文件不会覆盖已有同名文件，并会打印注册方式。`,
		Commands: []*cli.Command{
			{
				Name:      "handler",
				Usage:     "生成一个 HTTP handler 骨架",
				ArgsUsage: "<Name>",
				Action:    actionHandler,
			},
			{
				Name:      "job",
				Usage:     "生成一个定时任务骨架",
				ArgsUsage: "<Name>",
				Action:    actionJob,
			},
		},
	}
}

func actionHandler(ctx context.Context, c *cli.Command) error {
	name := c.Args().First()
	if name == "" {
		return cli.Exit("❌ 缺少名称，用法: beauty add handler <Name>", 1)
	}
	root, _, err := projectRoot()
	if err != nil {
		return cli.Exit(err.Error(), 1)
	}

	typeName := exportName(name)
	dir := filepath.Join(root, "internal", "endpoint", "handlers")
	if _, statErr := os.Stat(dir); statErr != nil {
		return cli.Exit(fmt.Sprintf("❌ 未找到 %s（当前项目可能未启用 HTTP 服务）", dir), 1)
	}
	outPath := filepath.Join(dir, fileName(name)+".go")

	content := fmt.Sprintf(`package handlers

import "net/http"

// %sHandler 处理 %s 相关请求
func %sHandler(w http.ResponseWriter, r *http.Request) {
	// TODO: 实现业务逻辑
	Success(w, map[string]string{"resource": %q})
}
`, typeName, name, typeName, strings.ToLower(name))

	if err := writeGoFile(outPath, content); err != nil {
		return cli.Exit(err.Error(), 1)
	}
	fmt.Printf("✅ 已生成: %s\n", outPath)
	fmt.Println("\n📋 在 internal/endpoint/router/router.go 的 routes() 中注册:")
	fmt.Printf("  {Method: \"GET\", URI: \"/%s\", Handler: handlers.%sHandler, Name: %q},\n",
		strings.ToLower(name), typeName, strings.ToLower(name))
	return nil
}

func actionJob(ctx context.Context, c *cli.Command) error {
	name := c.Args().First()
	if name == "" {
		return cli.Exit("❌ 缺少名称，用法: beauty add job <Name>", 1)
	}
	root, _, err := projectRoot()
	if err != nil {
		return cli.Exit(err.Error(), 1)
	}

	// 定时任务包可能位于 internal/job 或 internal/endpoint/job
	candidates := []string{
		filepath.Join(root, "internal", "job"),
		filepath.Join(root, "internal", "endpoint", "job"),
	}
	var dir string
	for _, d := range candidates {
		if _, statErr := os.Stat(d); statErr == nil {
			dir = d
			break
		}
	}
	if dir == "" {
		return cli.Exit("❌ 未找到定时任务目录（当前项目可能未启用 Cron 服务）", 1)
	}

	typeName := exportName(name)
	outPath := filepath.Join(dir, fileName(name)+".go")
	content := fmt.Sprintf(`package job

import (
	"context"
	"log/slog"
)

// %s %s 任务处理函数
func (c *CronJobs) %s(ctx context.Context) error {
	slog.Info("执行 %s 任务")
	// TODO: 实现任务逻辑
	return nil
}
`, typeName, name, typeName, strings.ToLower(name))

	if err := writeGoFile(outPath, content); err != nil {
		return cli.Exit(err.Error(), 1)
	}
	fmt.Printf("✅ 已生成: %s\n", outPath)
	fmt.Println("\n📋 在 GetOptions() 返回的列表中注册:")
	fmt.Printf("  cron.WithCronHandler(\"@every 1m\", c.%s),\n", typeName)
	return nil
}

// projectRoot 从当前目录读取 go.mod，返回项目根目录与模块名。
func projectRoot() (root, module string, err error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	data, err := os.ReadFile(filepath.Join(wd, "go.mod"))
	if err != nil {
		return "", "", fmt.Errorf("❌ 未在当前目录找到 go.mod，请在 Beauty 项目根目录运行")
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			module = strings.TrimSpace(rest)
			break
		}
	}
	return wd, module, nil
}

// writeGoFile 格式化并写入 Go 文件，已存在则报错避免覆盖。
func writeGoFile(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("❌ 文件已存在，跳过: %s", path)
	}
	out := []byte(content)
	if formatted, ferr := format.Source(out); ferr == nil {
		out = formatted
	}
	return os.WriteFile(path, out, 0o644)
}

// exportName 把名称首字母大写（导出标识符）。
func exportName(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// fileName 生成小写文件名。
func fileName(s string) string {
	return strings.ToLower(s)
}
