package mcpagent_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
	"github.com/rushteam/beauty/contrib/mcp"
	"github.com/rushteam/beauty/contrib/mcpagent"
)

type addIn struct {
	A int `json:"a" jsonschema:"first addend"`
	B int `json:"b" jsonschema:"second addend"`
}
type addOut struct {
	Sum int `json:"sum"`
}

// addServer 注册一个类型安全的 add 工具(In/Out → SDK 自动反射 JSON Schema)。
func addServer() *mcp.Server {
	s := mcp.NewServer("calc", "1.0.0")
	mcp.AddTool(s, &mcp.Tool{Name: "add", Description: "两数相加"},
		func(_ context.Context, _ *mcp.CallToolRequest, in addIn) (*mcp.CallToolResult, addOut, error) {
			sum := in.A + in.B
			return mcp.Text(strconv.Itoa(sum)), addOut{Sum: sum}, nil
		})
	return s
}

// dialInMemory 起进程内 MCP server 并连一个客户端会话。
func dialInMemory(t *testing.T, ctx context.Context) *mcp.ClientSession {
	t.Helper()
	clientT, serverT := sdk.NewInMemoryTransports()
	go func() { _ = addServer().Run(ctx, serverT) }()
	sess, err := mcp.Connect(ctx, "client", clientT)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// scriptedClient 是按脚本返回的假 llm.Client,记录最后一次请求以便断言工具结果被喂回。
type scriptedClient struct {
	steps []*llm.Response
	i     int
	last  llm.Request
}

func (c *scriptedClient) Generate(_ context.Context, req llm.Request) (*llm.Response, error) {
	c.last = req
	r := c.steps[c.i]
	c.i++
	return r, nil
}
func (c *scriptedClient) Stream(context.Context, llm.Request) (<-chan llm.Chunk, error) {
	return nil, errors.New("unused")
}

// 桥接:MCP 工具应转成带 schema 的 agent.Tool。
func TestTools_Bridge(t *testing.T) {
	ctx := context.Background()
	sess := dialInMemory(t, ctx)

	tools, err := mcpagent.Tools(ctx, sess)
	if err != nil {
		t.Fatalf("bridge: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("应桥接出 1 个工具, got %d", len(tools))
	}
	def := tools[0].Def
	if def.Name != "add" || def.Description != "两数相加" {
		t.Fatalf("tool def = %+v", def)
	}
	if !strings.Contains(string(def.Parameters), `"a"`) || !strings.Contains(string(def.Parameters), `"b"`) {
		t.Fatalf("入参 schema 未透传: %s", def.Parameters)
	}
}

// 直接调用桥接工具:参数转发到 MCP server,回传文本结果。
func TestTool_Call(t *testing.T) {
	ctx := context.Background()
	sess := dialInMemory(t, ctx)
	tools, _ := mcpagent.Tools(ctx, sess)

	out, err := tools[0].Call(ctx, json.RawMessage(`{"a":2,"b":3}`))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if out != "5" {
		t.Fatalf("结果 = %q, want 5", out)
	}
}

// 端到端:Runner 让模型请求调用 MCP 工具 → 桥接转发 → 结果喂回 → 模型给终态文本。
func TestRunner_WithMCPTools(t *testing.T) {
	ctx := context.Background()
	sess := dialInMemory(t, ctx)
	tools, _ := mcpagent.Tools(ctx, sess)

	fc := &scriptedClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "add", Arguments: json.RawMessage(`{"a":2,"b":3}`)}}},
		{Content: "结果是 5"},
	}}
	r := &agent.Runner{Client: fc, Tools: tools}

	resp, err := r.Run(ctx, llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.User, Content: "2+3=?"}}})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if resp.Content != "结果是 5" {
		t.Fatalf("final = %q", resp.Content)
	}
	// 第二轮请求里,MCP 工具的真实结果 "5" 应作为 tool 消息被喂回。
	last := fc.last.Messages
	tail := last[len(last)-1]
	if tail.Role != llm.Tool || tail.ToolCallID != "c1" || tail.Content != "5" {
		t.Fatalf("工具结果未正确喂回: %+v", tail)
	}
}

// 未知工具名 → CallTool 报错,桥接转成 error(交给 Runner 会喂回让模型自愈)。
func TestTool_ErrorPropagates(t *testing.T) {
	ctx := context.Background()
	sess := dialInMemory(t, ctx)
	bad := mcpagent.ToolFrom(sess, &mcp.Tool{Name: "nonexistent"})
	if _, err := bad.Call(ctx, json.RawMessage(`{}`)); err == nil {
		t.Fatal("调用不存在的 MCP 工具应返回 error")
	}
}
