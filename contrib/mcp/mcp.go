// Package mcp 是 beauty 的 Model Context Protocol 集成:把服务能力暴露成 MCP 工具/资源/提示,
// 让 AI 客户端(Claude、IDE、各类 Agent)调用;也能作为 MCP 客户端消费别的 server。
// 作为**独立 Go 模块**发布(github.com/rushteam/beauty/contrib/mcp),薄封装官方
// github.com/modelcontextprotocol/go-sdk——工具入参/出参的 JSON Schema 由 SDK 泛型自动反射生成
// (AddTool[In,Out]),协议/传输/会话都跟随官方演进。不 import beauty 核心。
//
// 两种部署:
//   - 远程:HTTPHandler(server) 是 http.Handler(Streamable HTTP),挂到 beauty.WithWebServer;
//   - 本地工具:NewStdioService(server) 结构上满足 beauty.Service,或 server.Run(ctx, &StdioTransport{})。
//
// 客户端:DialHTTP / DialCommand / Connect 得到会话,ListTools/CallTool 调用。
//
// 边界(机制而非策略):工具的业务逻辑、鉴权(HTTP 端点外层套 auth 中间件)、OAuth、
// 具体 schema 都由使用方定;本包只做"协议 + 注册 + 传输 + beauty 挂载"。
package mcp

import (
	"context"
	"net/http"
	"os/exec"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// 复用 SDK 类型,让使用方只 import 本包即可拿到常用类型。
type (
	Server          = sdk.Server
	Tool            = sdk.Tool
	CallToolRequest = sdk.CallToolRequest
	CallToolResult  = sdk.CallToolResult
	Content         = sdk.Content
	TextContent     = sdk.TextContent
	ClientSession   = sdk.ClientSession
	Transport       = sdk.Transport
)

// NewServer 创建一个 MCP server(name/version 会在 initialize 握手里报给客户端)。
func NewServer(name, version string) *Server {
	return sdk.NewServer(&sdk.Implementation{Name: name, Version: version}, nil)
}

// AddTool 注册一个类型安全的工具:In/Out 是 Go 结构体,SDK 自动按其字段(json/jsonschema 标签)
// 反射生成入参/出参 JSON Schema 并做校验。handler 返回文本结果用 Text(...),或返回结构化 Out
// 让 SDK 生成 structured content。
func AddTool[In, Out any](s *Server, t *Tool, handler sdk.ToolHandlerFor[In, Out]) {
	sdk.AddTool(s, t, handler)
}

// Text 构造一个纯文本工具结果。
func Text(s string) *CallToolResult {
	return &CallToolResult{Content: []Content{&TextContent{Text: s}}}
}

// HTTPHandler 返回把 server 以 Streamable HTTP 暴露的 http.Handler,挂到 beauty webserver:
//
//	mux.Handle("/mcp", mcp.HTTPHandler(srv))  // 鉴权由外层中间件负责(policy)
func HTTPHandler(s *Server) http.Handler {
	return sdk.NewStreamableHTTPHandler(func(*http.Request) *Server { return s }, nil)
}

// Service 以 stdio 运行 MCP server,结构上满足 beauty.Service(Start/String),可
// beauty.WithService(mcp.NewStdioService(srv, "x")) 挂进框架、随 app 停机。
type Service struct {
	s    *Server
	name string
}

// NewStdioService 把 server 包成 stdio 传输的 Service(本地工具场景,如被 Claude Desktop 拉起)。
func NewStdioService(s *Server, name string) *Service {
	if name == "" {
		name = "mcp"
	}
	return &Service{s: s, name: name}
}

// Start 运行 server 直到 ctx 取消——满足 beauty.Service。
func (svc *Service) Start(ctx context.Context) error { return svc.s.Run(ctx, &sdk.StdioTransport{}) }

// String 满足 beauty.Service。
func (svc *Service) String() string { return "mcp.Service(" + svc.name + "@stdio)" }

// ===== 客户端 =====

func newClient(name string) *sdk.Client {
	return sdk.NewClient(&sdk.Implementation{Name: name, Version: "1.0.0"}, nil)
}

// DialHTTP 连接一个远程 MCP server(Streamable HTTP)。
func DialHTTP(ctx context.Context, name, endpoint string) (*ClientSession, error) {
	return newClient(name).Connect(ctx, &sdk.StreamableClientTransport{Endpoint: endpoint}, nil)
}

// DialCommand 拉起一个子进程 MCP server 并经其 stdio 连接(本地工具)。
func DialCommand(ctx context.Context, name string, cmd *exec.Cmd) (*ClientSession, error) {
	return newClient(name).Connect(ctx, &sdk.CommandTransport{Command: cmd}, nil)
}

// Connect 用任意 Transport 连接(如测试用的 InMemory)。
func Connect(ctx context.Context, name string, t Transport) (*ClientSession, error) {
	return newClient(name).Connect(ctx, t, nil)
}
