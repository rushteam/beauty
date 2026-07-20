package mcp_test

import (
	"context"
	"net/http/httptest"
	"strconv"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rushteam/beauty/contrib/mcp"
)

type addIn struct {
	A int `json:"a" jsonschema:"first addend"`
	B int `json:"b" jsonschema:"second addend"`
}
type addOut struct {
	Sum int `json:"sum"`
}

// buildServer 注册一个类型安全的 add 工具(In/Out 结构体 → SDK 自动反射 JSON Schema)。
func buildServer(t *testing.T) *mcp.Server {
	t.Helper()
	s := mcp.NewServer("test-server", "1.0.0")
	mcp.AddTool(s, &mcp.Tool{Name: "add", Description: "两数相加"},
		func(_ context.Context, _ *mcp.CallToolRequest, in addIn) (*mcp.CallToolResult, addOut, error) {
			sum := in.A + in.B
			return mcp.Text(strconv.Itoa(sum)), addOut{Sum: sum}, nil
		})
	return s
}

func assertAdd(t *testing.T, sess *mcp.ClientSession) {
	t.Helper()
	ctx := context.Background()

	// tools/list 应含 add,且带反射出的入参 schema。
	lt, err := sess.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	var found *mcp.Tool
	for _, tl := range lt.Tools {
		if tl.Name == "add" {
			found = tl
		}
	}
	if found == nil {
		t.Fatal("未列出 add 工具")
	}
	if found.InputSchema == nil {
		t.Fatal("add 工具应自带反射生成的 InputSchema")
	}

	// tools/call add(2,3) → 文本结果 "5"。
	res, err := sess.CallTool(ctx, &sdk.CallToolParams{Name: "add", Arguments: map[string]any{"a": 2, "b": 3}})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if res.IsError {
		t.Fatalf("工具返回错误: %+v", res)
	}
	if len(res.Content) == 0 {
		t.Fatal("结果无 content")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok || tc.Text != "5" {
		t.Fatalf("结果文本 = %+v, want 5", res.Content[0])
	}
}

// 进程内传输:server 与 client 直连,验证注册/列举/调用与 struct→schema 反射。
func TestInMemory_ToolCall(t *testing.T) {
	srv := buildServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clientT, serverT := sdk.NewInMemoryTransports()
	go func() { _ = srv.Run(ctx, serverT) }()

	sess, err := mcp.Connect(ctx, "client", clientT)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer sess.Close()
	assertAdd(t, sess)
}

// Streamable HTTP:HTTPHandler 挂 httptest,DialHTTP 客户端端到端调用。
func TestHTTP_ToolCall(t *testing.T) {
	srv := buildServer(t)
	httpSrv := httptest.NewServer(mcp.HTTPHandler(srv))
	defer httpSrv.Close()

	ctx := context.Background()
	sess, err := mcp.DialHTTP(ctx, "client", httpSrv.URL)
	if err != nil {
		t.Fatalf("dial http: %v", err)
	}
	defer sess.Close()
	assertAdd(t, sess)
}

// Service 结构上满足 beauty.Service(编译期断言 Start/String)。
func TestServiceShape(t *testing.T) {
	var svc interface {
		Start(context.Context) error
		String() string
	} = mcp.NewStdioService(buildServer(t), "demo")
	if svc.String() == "" {
		t.Fatal("String 不应为空")
	}
}
