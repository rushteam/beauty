package llm_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/anthropic"
	"github.com/rushteam/beauty/contrib/llm/openai"
)

var weatherTool = llm.ToolDef{
	Name:        "get_weather",
	Description: "查询某城市天气",
	Parameters:  json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`),
}

// OpenAI:请求应携带 tools/tool_choice,响应里的 tool_calls 应解析到 Response.ToolCalls。
func TestOpenAI_ToolCall(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = io.WriteString(w, `{"model":"gpt-4o","choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SF\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":3}}`)
	}))
	defer srv.Close()

	c := openai.New("k", openai.WithBaseURL(srv.URL))
	resp, err := c.Generate(context.Background(), llm.Request{
		Model:      "gpt-4o",
		Messages:   []llm.Message{{Role: llm.User, Content: "天气?"}},
		Tools:      []llm.ToolDef{weatherTool},
		ToolChoice: "auto",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.Contains(gotBody, `"tools"`) || !strings.Contains(gotBody, "get_weather") {
		t.Fatalf("请求体应带 tools: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"tool_choice":"auto"`) {
		t.Fatalf("请求体应带 tool_choice=auto: %s", gotBody)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("应解析出 1 个 tool call, got %+v", resp.ToolCalls)
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "get_weather" || !strings.Contains(string(tc.Arguments), "SF") {
		t.Fatalf("tool call = %+v", tc)
	}
	if resp.StopReason != "tool_calls" {
		t.Fatalf("stop reason = %q", resp.StopReason)
	}
}

// OpenAI:回传工具结果时,assistant 的 tool_calls 与 role=tool 的 tool_call_id 应正确序列化。
func TestOpenAI_ToolResultRoundTrip(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = io.WriteString(w, `{"model":"gpt-4o","choices":[{"message":{"content":"晴,25°C"},"finish_reason":"stop"}],"usage":{}}`)
	}))
	defer srv.Close()

	c := openai.New("k", openai.WithBaseURL(srv.URL))
	_, err := c.Generate(context.Background(), llm.Request{
		Model: "gpt-4o",
		Messages: []llm.Message{
			{Role: llm.User, Content: "天气?"},
			{Role: llm.Assistant, ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "get_weather", Arguments: json.RawMessage(`{"city":"SF"}`)}}},
			{Role: llm.Tool, ToolCallID: "call_1", Content: "晴,25°C"},
		},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.Contains(gotBody, `"tool_call_id":"call_1"`) {
		t.Fatalf("请求体应带 tool_call_id: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"role":"tool"`) {
		t.Fatalf("请求体应带 role=tool: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"arguments":"{\"city\":\"SF\"}"`) {
		t.Fatalf("assistant tool_calls 的 arguments 应为 JSON 字符串: %s", gotBody)
	}
}

// 纯文本请求体应与旧版一致(不含 tools 字段),保证向后兼容。
func TestOpenAI_TextOnlyBodyUnchanged(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = io.WriteString(w, `{"model":"gpt-4o","choices":[{"message":{"content":"hi"}}],"usage":{}}`)
	}))
	defer srv.Close()
	c := openai.New("k", openai.WithBaseURL(srv.URL))
	_, _ = c.Generate(context.Background(), llm.Request{Model: "gpt-4o", Messages: []llm.Message{{Role: llm.User, Content: "hi"}}})
	if strings.Contains(gotBody, "tools") || strings.Contains(gotBody, "tool_choice") || strings.Contains(gotBody, "tool_calls") {
		t.Fatalf("纯文本请求体不应含工具字段: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"messages":[{"role":"user","content":"hi"}]`) {
		t.Fatalf("纯文本消息序列化异常: %s", gotBody)
	}
}

// Anthropic:请求 tools 用 input_schema;响应 tool_use 应解析;tool 往返用 content blocks。
func TestAnthropic_ToolCall(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = io.WriteString(w, `{"model":"claude-x","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"SF"}}],"usage":{"input_tokens":4,"output_tokens":2}}`)
	}))
	defer srv.Close()

	c := anthropic.New("k", anthropic.WithBaseURL(srv.URL))
	resp, err := c.Generate(context.Background(), llm.Request{
		Model:    "claude-x",
		Messages: []llm.Message{{Role: llm.User, Content: "天气?"}},
		Tools:    []llm.ToolDef{weatherTool},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.Contains(gotBody, `"input_schema"`) || !strings.Contains(gotBody, "get_weather") {
		t.Fatalf("请求体应带 tools+input_schema: %s", gotBody)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "get_weather" || resp.ToolCalls[0].ID != "toolu_1" {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	if !strings.Contains(string(resp.ToolCalls[0].Arguments), "SF") {
		t.Fatalf("arguments = %s", resp.ToolCalls[0].Arguments)
	}
}

// Anthropic:tool 结果并入 user 回合、assistant 调用转 tool_use 块。
func TestAnthropic_ToolResultRoundTrip(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = io.WriteString(w, `{"model":"claude-x","stop_reason":"end_turn","content":[{"type":"text","text":"晴"}],"usage":{}}`)
	}))
	defer srv.Close()

	c := anthropic.New("k", anthropic.WithBaseURL(srv.URL))
	_, err := c.Generate(context.Background(), llm.Request{
		Model: "claude-x",
		Messages: []llm.Message{
			{Role: llm.User, Content: "天气?"},
			{Role: llm.Assistant, ToolCalls: []llm.ToolCall{{ID: "toolu_1", Name: "get_weather", Arguments: json.RawMessage(`{"city":"SF"}`)}}},
			{Role: llm.Tool, ToolCallID: "toolu_1", Content: "晴"},
		},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.Contains(gotBody, `"type":"tool_use"`) || !strings.Contains(gotBody, `"type":"tool_result"`) {
		t.Fatalf("请求体应含 tool_use 与 tool_result 块: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"tool_use_id":"toolu_1"`) {
		t.Fatalf("tool_result 应带 tool_use_id: %s", gotBody)
	}
}
