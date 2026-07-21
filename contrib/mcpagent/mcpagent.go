// Package mcpagent 把一个 MCP 客户端会话(contrib/mcp)上的远程工具桥接成
// contrib/llm/agent 的 Tool,于是 MCP server 暴露的工具能直接喂给 agent.Runner 驱动的
// LLM 工具循环——模型请求调用 → 转发到 MCP server → 结果回传给模型。
//
// 这是刻意独立的"胶水"模块:它同时依赖 mcp 与 llm/agent,好让那两个模块彼此保持零耦合
// (llm 不 import mcp、mcp 不 import llm,各自可单独使用)。工具的 schema/描述由 MCP 端提供,
// 本包只做"列举 + 名称/入参透传 + 调用转发 + 文本结果聚合",不掺业务。
package mcpagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
	"github.com/rushteam/beauty/contrib/mcp"
)

// Tools 列举 session 上的全部 MCP 工具,各自桥接成一个 agent.Tool。
// 通常在建立会话后调一次,把结果放进 agent.Runner.Tools。
func Tools(ctx context.Context, sess *mcp.ClientSession) ([]agent.Tool, error) {
	lt, err := sess.ListTools(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("mcpagent: list tools: %w", err)
	}
	tools := make([]agent.Tool, 0, len(lt.Tools))
	for _, t := range lt.Tools {
		tools = append(tools, ToolFrom(sess, t))
	}
	return tools, nil
}

// ToolFrom 把单个 MCP 工具描述桥接成 agent.Tool:名字/描述/入参 schema 原样透传,
// Call 时把模型给的入参(JSON)转发到 MCP server,并把返回的文本内容聚合为字符串结果。
// MCP 侧报错(res.IsError 或传输错误)转成 Go error——交给 Runner 会作为错误文本喂回模型自愈。
func ToolFrom(sess *mcp.ClientSession, t *mcp.Tool) agent.Tool {
	var params json.RawMessage
	if t.InputSchema != nil { // InputSchema 是 any(线上为 JSON Schema 对象),序列化即得 ToolDef.Parameters
		if b, err := json.Marshal(t.InputSchema); err == nil {
			params = b
		}
	}
	name := t.Name
	return agent.Tool{
		Def: llm.ToolDef{Name: name, Description: t.Description, Parameters: params},
		Call: func(ctx context.Context, args json.RawMessage) (string, error) {
			var arguments any
			if len(args) > 0 {
				arguments = args // json.RawMessage 原样序列化为 JSON,直接作为 MCP 入参
			}
			res, err := sess.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: arguments})
			if err != nil {
				return "", err
			}
			text := textOf(res)
			if res.IsError {
				return "", fmt.Errorf("mcp tool %q: %s", name, text)
			}
			return text, nil
		},
	}
}

// textOf 聚合结果里的文本内容块(忽略图片/音频等非文本块)。
func textOf(res *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}
