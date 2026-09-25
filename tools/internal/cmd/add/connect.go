package add

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"
)

// connectPluginRemote 是 buf 远程插件 protoc-gen-connect-go。
const connectPluginRemote = "buf.build/connectrpc/go:v1.18.1"

func connectCommand() *cli.Command {
	return &cli.Command{
		Name:  "connect",
		Usage: "为项目启用 Connect 协议(buf 插件 + contrib/connectrpc 服务骨架)",
		Description: `为基于 protobuf 的项目启用 Connect (兼容 gRPC / gRPC-Web / Connect 三协议)：
   • 向 buf.gen.yaml 追加 protoc-gen-connect-go 插件
   • 生成 internal/endpoint/connect/server.go 服务骨架
   • go get github.com/rushteam/beauty/contrib/connectrpc`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "no-get",
				Usage: "不执行 go get",
			},
		},
		Action: actionConnect,
	}
}

func actionConnect(ctx context.Context, c *cli.Command) error {
	root, _, err := projectRoot()
	if err != nil {
		return cli.Exit(err.Error(), 1)
	}

	bufGen := filepath.Join(root, "buf.gen.yaml")
	data, err := os.ReadFile(bufGen)
	switch {
	case err != nil:
		fmt.Println("⚠️  未找到 buf.gen.yaml，跳过插件配置(可先 `beauty new . --grpc` 生成)")
	default:
		updated, changed := addConnectPlugin(string(data))
		if changed {
			if err := os.WriteFile(bufGen, []byte(updated), 0o644); err != nil {
				return cli.Exit(fmt.Sprintf("❌ 写入 buf.gen.yaml 失败: %v", err), 1)
			}
			fmt.Printf("✅ buf.gen.yaml 已追加插件 %s\n", connectPluginRemote)
		} else {
			fmt.Println("ℹ️  buf.gen.yaml 已包含 connect 插件，跳过")
		}
	}

	dir := filepath.Join(root, "internal", "endpoint", "connect")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return cli.Exit(fmt.Sprintf("❌ 创建目录失败: %v", err), 1)
	}
	outPath := filepath.Join(dir, "server.go")
	if err := writeGoFile(outPath, connectServerSource); err != nil {
		fmt.Println(err.Error())
	} else {
		fmt.Println("✅ 已生成: internal/endpoint/connect/server.go")
	}

	if !c.Bool("no-get") {
		cmd := exec.CommandContext(ctx, "go", "get", contribModulePrefix+"connectrpc@latest")
		cmd.Dir = root
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return cli.Exit(fmt.Sprintf("❌ go get 失败: %v", err), 1)
		}
	}

	fmt.Print(`
📋 后续步骤:
  1. buf generate                      # 生成 *connect/*.connect.go
  2. 在 internal/endpoint/connect/server.go 中用 srv.Handle 注册生成的 handler
  3. 在 main.go 中加入: beauty.WithService(connect.New(":8081"))
`)
	return nil
}

// addConnectPlugin 向 buf.gen.yaml 的 plugins 列表追加 connect-go 插件，输出目录沿用第一个插件的 out。
// 已包含 connect 插件时原样返回且 changed=false。
func addConnectPlugin(content string) (updated string, changed bool) {
	if strings.Contains(content, "connectrpc/go") || strings.Contains(content, "connect-go") {
		return content, false
	}
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	out := "api/v1"
	for _, line := range lines {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "out:"); ok {
			out = strings.TrimSpace(v)
			break
		}
	}
	plugin := []string{
		"  - remote: " + connectPluginRemote,
		"    out: " + out,
		"    opt:",
		"      - paths=source_relative",
	}

	start := -1
	for i, line := range lines {
		if strings.TrimRight(line, " ") == "plugins:" {
			start = i
			break
		}
	}
	if start < 0 {
		lines = append(lines, "plugins:")
		lines = append(lines, plugin...)
		return strings.Join(lines, "\n") + "\n", true
	}

	// plugins 块在下一个顶层键(无缩进的非空、非注释行)处结束
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		l := lines[i]
		if l != "" && l[0] != ' ' && l[0] != '\t' && l[0] != '#' {
			end = i
			break
		}
	}
	for end > start+1 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	result := append([]string{}, lines[:end]...)
	result = append(result, plugin...)
	result = append(result, lines[end:]...)
	return strings.Join(result, "\n") + "\n", true
}

const connectServerSource = `package connect

import (
	"github.com/rushteam/beauty/contrib/connectrpc"
)

// New 创建 Connect 服务(默认启用 H2C 与 gRPC 健康检查)，同一端口同时接受 Connect、gRPC、gRPC-Web 请求。
func New(addr string, opts ...connectrpc.Option) *connectrpc.Server {
	srv := connectrpc.New(addr, opts...)

	// 注册 protoc-gen-connect-go 生成的 handler，例如:
	//   srv.Handle(userv1connect.NewUserServiceHandler(service.NewUserService()))

	return srv
}
`
