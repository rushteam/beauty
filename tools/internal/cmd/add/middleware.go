package add

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"
)

func middlewareCommand() *cli.Command {
	return &cli.Command{
		Name:      "middleware",
		Aliases:   []string{"mw"},
		Usage:     "生成一个中间件骨架(HTTP，可选 gRPC 拦截器)",
		ArgsUsage: "<Name>",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "grpc",
				Usage: "同时生成 gRPC UnaryServerInterceptor",
			},
		},
		Action: actionMiddleware,
	}
}

// middlewareDirs 是按优先级查找的中间件目录，均不存在时创建第一个。
var middlewareDirs = []string{
	filepath.Join("internal", "infra", "middleware"),
	filepath.Join("internal", "middleware"),
	filepath.Join("internal", "adapter", "http", "middleware"),
}

func actionMiddleware(ctx context.Context, c *cli.Command) error {
	name := c.Args().First()
	if name == "" {
		return cli.Exit("❌ 缺少名称，用法: beauty add middleware <Name> [--grpc]", 1)
	}
	root, _, err := projectRoot()
	if err != nil {
		return cli.Exit(err.Error(), 1)
	}

	dir := filepath.Join(root, middlewareDirs[0])
	for _, d := range middlewareDirs {
		if _, statErr := os.Stat(filepath.Join(root, d)); statErr == nil {
			dir = filepath.Join(root, d)
			break
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return cli.Exit(fmt.Sprintf("❌ 创建目录失败: %v", err), 1)
	}

	typeName := exportName(name)
	withGrpc := c.Bool("grpc")
	outPath := filepath.Join(dir, fileName(name)+".go")
	if err := writeGoFile(outPath, middlewareSource(filepath.Base(dir), typeName, withGrpc)); err != nil {
		return cli.Exit(err.Error(), 1)
	}

	rel, _ := filepath.Rel(root, outPath)
	fmt.Printf("✅ 已生成: %s\n", rel)
	fmt.Println("\n📋 注册方式:")
	fmt.Printf("  webserver.WithMiddleware(middleware.%s())\n", typeName)
	if withGrpc {
		fmt.Printf("  grpcserver.WithGrpcServerUnaryInterceptor(middleware.%sUnaryInterceptor())\n", typeName)
	}
	return nil
}

func middlewareSource(pkg, typeName string, withGrpc bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\n", pkg)
	if withGrpc {
		b.WriteString("import (\n\t\"context\"\n\t\"net/http\"\n\n\t\"google.golang.org/grpc\"\n)\n\n")
	} else {
		b.WriteString("import \"net/http\"\n\n")
	}
	fmt.Fprintf(&b, `// %[1]s 返回 %[1]s HTTP 中间件，可直接传给 webserver.WithMiddleware。
func %[1]s() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}
`, typeName)
	if withGrpc {
		fmt.Fprintf(&b, `
// %[1]sUnaryInterceptor 返回 %[1]s gRPC 一元拦截器，可传给 grpcserver.WithGrpcServerUnaryInterceptor。
func %[1]sUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		return handler(ctx, req)
	}
}
`, typeName)
	}
	return b.String()
}
