package add

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"
)

const contribModulePrefix = "github.com/rushteam/beauty/contrib/"

// contribInfo 描述一个可 `beauty add contrib` 引入的 contrib 模块。
type contribInfo struct {
	Name    string
	Summary string
	Usage   string // 引入后的最小用法提示，可为空
}

var contribCatalog = []contribInfo{
	{Name: "a2a", Summary: "A2A (Agent-to-Agent) 协议服务端/客户端"},
	{Name: "agones", Summary: "Agones GameServer 生命周期 × gameroom"},
	{Name: "agui", Summary: "AG-UI SSE 流式协议"},
	{Name: "bun", Summary: "推荐默认 ORM：读写句柄、连接池、DB 弹性"},
	{Name: "casbin", Summary: "authz.Enforcer 的 Casbin 实现(RBAC/ABAC)"},
	{Name: "codec/gozero", Summary: "go-zero 注册中心格式编解码"},
	{Name: "codec/kitex", Summary: "Kitex 注册中心格式编解码"},
	{Name: "codec/kratos", Summary: "Kratos 注册中心格式编解码"},
	{Name: "connectrpc", Summary: "Connect 协议服务端(兼容 gRPC/gRPC-Web)+ 服务发现客户端",
		Usage: `srv := connectrpc.New(":8080")
srv.Handle(pingv1connect.NewPingServiceHandler(&PingServer{}))
app := beauty.New(beauty.WithService(srv))`},
	{Name: "console", Summary: "Web 远程控制台(WebSocket 交互式命令行)"},
	{Name: "elasticsearch", Summary: "Elasticsearch 健康/搜索/写入"},
	{Name: "ginadapt", Summary: "beauty HTTP 中间件 → gin.HandlerFunc 适配"},
	{Name: "gorm", Summary: "GORM：读写分离、otelgorm、slog 日志、DB 弹性"},
	{Name: "graphql", Summary: "gqlgen GraphQL/BFF 服务"},
	{Name: "kafka", Summary: "mq.Publisher/Subscriber 的 Kafka 实现(franz-go)",
		Usage: `pub, err := kafka.NewPublisher([]string{"localhost:9092"})
sub := kafka.NewSubscriber([]string{"localhost:9092"}) // pub/sub 实现 pkg/messaging/mq 接口`},
	{Name: "kitex", Summary: "Kitex Thrift 服务端 + 服务发现 Resolver"},
	{Name: "llm", Summary: "provider 无关 LLM 客户端 + agent 循环"},
	{Name: "llmcheckpoint", Summary: "agent RunStore/CheckpointStore 持久化(SQLite/Redis)"},
	{Name: "llmservice", Summary: "把 llm/agent 包装为 beauty.Service"},
	{Name: "llmsession", Summary: "agent 会话存储(SQLite/Redis)"},
	{Name: "mcp", Summary: "Model Context Protocol server/client"},
	{Name: "mcpagent", Summary: "MCP 远程工具 → llm/agent.Tool"},
	{Name: "memoryvector", Summary: "Embedder + vector → agent 语义长期记忆"},
	{Name: "modbus", Summary: "Modbus 采集器 + MQ 桥接"},
	{Name: "mqtt", Summary: "mq.Publisher/Subscriber 的 MQTT 实现"},
	{Name: "nats", Summary: "mq 的 NATS 实现(at-most-once)"},
	{Name: "natsjs", Summary: "mq 的 NATS JetStream 实现(at-least-once)"},
	{Name: "opcua", Summary: "OPC-UA 客户端/订阅/轮询"},
	{Name: "openfga", Summary: "authz.Enforcer 的 OpenFGA 实现(ReBAC)"},
	{Name: "otelllm", Summary: "LLM OTel Trace/Metrics(GenAI 语义约定)"},
	{Name: "p2p-webrtc", Summary: "p2p 的 WebRTC DataChannel 传输"},
	{Name: "proxywasm", Summary: "Proxy-Wasm 插件作为 HTTP 中间件运行"},
	{Name: "rabbitmq", Summary: "mq 的 RabbitMQ 实现"},
	{Name: "redisqueue", Summary: "Redis 分布式任务队列(BullMQ 风格)"},
	{Name: "redisstream", Summary: "mq 的 Redis Streams 实现"},
	{Name: "spire", Summary: "SPIFFE/SPIRE X509-SVID mTLS"},
	{Name: "sqldb", Summary: "database/sql 读写分离 + OTel + DB 弹性"},
	{Name: "vector", Summary: "向量存储 / RAG 检索"},
	{Name: "wasm", Summary: "wazero WebAssembly 插件运行时"},
	{Name: "wasmagent", Summary: "wasm 沙箱执行接入 llm/agent"},
	{Name: "wasmopa", Summary: "OPA Rego wasm 实现 authz.Enforcer"},
}

func findContrib(name string) (contribInfo, bool) {
	name = strings.TrimPrefix(strings.Trim(name, "/"), contribModulePrefix)
	i := slices.IndexFunc(contribCatalog, func(c contribInfo) bool { return c.Name == name })
	if i < 0 {
		return contribInfo{}, false
	}
	return contribCatalog[i], true
}

// suggestContrib 返回名称包含 name 的候选模块，用于拼写错误时提示。
func suggestContrib(name string) []string {
	var out []string
	for _, c := range contribCatalog {
		if strings.Contains(c.Name, name) || strings.Contains(name, c.Name) {
			out = append(out, c.Name)
		}
	}
	return out
}

func contribCommand() *cli.Command {
	return &cli.Command{
		Name:      "contrib",
		Usage:     "引入 contrib 模块(go get)，不带参数时列出全部模块",
		ArgsUsage: "[name...]",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "version",
				Usage: "模块版本，如 v0.9.6",
				Value: "latest",
			},
			&cli.BoolFlag{
				Name:  "dry-run",
				Usage: "只打印将执行的命令",
			},
		},
		Action: actionContrib,
	}
}

func actionContrib(ctx context.Context, c *cli.Command) error {
	names := c.Args().Slice()
	if len(names) == 0 {
		printContribCatalog()
		return nil
	}
	if _, _, err := projectRoot(); err != nil {
		return cli.Exit(err.Error(), 1)
	}

	version := c.String("version")
	var infos []contribInfo
	var args []string
	for _, n := range names {
		info, ok := findContrib(n)
		if !ok {
			msg := fmt.Sprintf("❌ 未知的 contrib 模块: %s", n)
			if s := suggestContrib(n); len(s) > 0 {
				msg += "\n💡 是否想要: " + strings.Join(s, ", ")
			}
			return cli.Exit(msg+"\n运行 `beauty add contrib` 查看全部模块", 1)
		}
		infos = append(infos, info)
		args = append(args, contribModulePrefix+info.Name+"@"+version)
	}

	fmt.Printf("📦 go get %s\n", strings.Join(args, " "))
	if !c.Bool("dry-run") {
		cmd := exec.CommandContext(ctx, "go", append([]string{"get"}, args...)...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return cli.Exit(fmt.Sprintf("❌ go get 失败: %v", err), 1)
		}
	}

	for _, info := range infos {
		fmt.Printf("\n✅ %s — %s\n", info.Name, info.Summary)
		fmt.Printf("   import \"%s%s\"\n", contribModulePrefix, info.Name)
		if info.Usage != "" {
			for line := range strings.SplitSeq(info.Usage, "\n") {
				fmt.Printf("   %s\n", line)
			}
		}
		fmt.Printf("   📖 https://github.com/rushteam/beauty/tree/main/contrib/%s\n", info.Name)
	}
	return nil
}

func printContribCatalog() {
	width := 0
	for _, c := range contribCatalog {
		width = max(width, len(c.Name))
	}
	fmt.Println("📦 可用的 contrib 模块(beauty add contrib <name>):")
	for _, c := range contribCatalog {
		fmt.Printf("  %-*s  %s\n", width, c.Name, c.Summary)
	}
}
