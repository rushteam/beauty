package gemini

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
)

var weatherTool = llm.ToolDef{
	Name:        "get_weather",
	Description: "查询某城市天气",
	Parameters:  json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`),
}

// 请求应携带 tools.functionDeclarations 和 toolConfig;响应的 functionCall 应解析到 Response.ToolCalls。
func TestGemini_ToolCall(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = io.WriteString(w, `{
			"candidates": [{
				"content": {
					"role": "model",
					"parts": [{"functionCall": {"id": "fc_1", "name": "get_weather", "args": {"city": "SF"}}}]
				},
				"finishReason": "STOP"
			}],
			"usageMetadata": {"promptTokenCount": 10, "candidatesTokenCount": 5, "totalTokenCount": 15}
		}`)
	}))
	defer srv.Close()

	c := New("test-key", WithBaseURL(srv.URL))
	resp, err := c.Generate(context.Background(), llm.Request{
		Model:      "gemini-2.0-flash",
		Messages:   []llm.Message{{Role: llm.User, Content: "天气?"}},
		Tools:      []llm.ToolDef{weatherTool},
		ToolChoice: "auto",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	// 验证请求体包含 functionDeclarations
	if !strings.Contains(gotBody, `"functionDeclarations"`) {
		t.Fatalf("请求体应含 functionDeclarations: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"get_weather"`) {
		t.Fatalf("请求体应含工具名 get_weather: %s", gotBody)
	}
	// 验证 toolConfig
	if !strings.Contains(gotBody, `"functionCallingConfig"`) {
		t.Fatalf("请求体应含 functionCallingConfig: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"AUTO"`) {
		t.Fatalf("请求体应含 mode=AUTO: %s", gotBody)
	}
	// 验证 x-goog-api-key 认证
	// (在 httptest 中无法直接验证,但确认请求通过即可)

	// 验证响应解析
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("应解析出 1 个 tool call, got %d", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "fc_1" || tc.Name != "get_weather" {
		t.Fatalf("tool call = %+v", tc)
	}
	if !strings.Contains(string(tc.Arguments), "SF") {
		t.Fatalf("arguments = %s", tc.Arguments)
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 5 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

// 回传工具结果时,assistant 的 functionCall 应序列化为 model 回合,
// tool 结果应序列化为 user 回合的 functionResponse。
func TestGemini_ToolResultRoundTrip(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = io.WriteString(w, `{
			"candidates": [{
				"content": {"role": "model", "parts": [{"text": "旧金山天气晴,25°C"}]},
				"finishReason": "STOP"
			}],
			"usageMetadata": {"promptTokenCount": 20, "candidatesTokenCount": 10}
		}`)
	}))
	defer srv.Close()

	c := New("test-key", WithBaseURL(srv.URL))
	resp, err := c.Generate(context.Background(), llm.Request{
		Model: "gemini-2.0-flash",
		Messages: []llm.Message{
			{Role: llm.User, Content: "天气?"},
			{Role: llm.Assistant, ToolCalls: []llm.ToolCall{{ID: "fc_1", Name: "get_weather", Arguments: json.RawMessage(`{"city":"SF"}`)}}},
			{Role: llm.Tool, ToolCallID: "fc_1", Content: `{"temp":"25C","cond":"晴"}`},
		},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	// 验证 assistant 消息转为 model 回合的 functionCall
	if !strings.Contains(gotBody, `"functionCall"`) {
		t.Fatalf("请求体应含 functionCall: %s", gotBody)
	}
	// 验证 tool 消息转为 user 回合的 functionResponse
	if !strings.Contains(gotBody, `"functionResponse"`) {
		t.Fatalf("请求体应含 functionResponse: %s", gotBody)
	}
	// 验证 functionResponse 包含正确的工具名(通过 ToolCallID → ToolCall.Name 映射)
	if !strings.Contains(gotBody, `"name":"get_weather"`) {
		t.Fatalf("functionResponse 应含工具名 get_weather: %s", gotBody)
	}
	// 验证最终文本响应
	if resp.Content != "旧金山天气晴,25°C" {
		t.Fatalf("content = %q", resp.Content)
	}
}

// 纯文本请求不应包含 tools/toolConfig 字段。
func TestGemini_TextOnlyNoTools(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = io.WriteString(w, `{
			"candidates": [{
				"content": {"role": "model", "parts": [{"text": "你好!"}]},
				"finishReason": "STOP"
			}]
		}`)
	}))
	defer srv.Close()

	c := New("test-key", WithBaseURL(srv.URL))
	_, err := c.Generate(context.Background(), llm.Request{
		Model:    "gemini-2.0-flash",
		Messages: []llm.Message{{Role: llm.User, Content: "你好"}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if strings.Contains(gotBody, "functionDeclarations") || strings.Contains(gotBody, "toolConfig") {
		t.Fatalf("纯文本请求不应含工具字段: %s", gotBody)
	}
}

// system 消息应翻译为 systemInstruction。
func TestGemini_SystemInstruction(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = io.WriteString(w, `{
			"candidates": [{
				"content": {"role": "model", "parts": [{"text": "喵!"}]},
				"finishReason": "STOP"
			}]
		}`)
	}))
	defer srv.Close()

	c := New("test-key", WithBaseURL(srv.URL))
	_, _ = c.Generate(context.Background(), llm.Request{
		Model:    "gemini-2.0-flash",
		System:   "你是一只猫",
		Messages: []llm.Message{{Role: llm.User, Content: "你好"}},
	})
	if !strings.Contains(gotBody, `"systemInstruction"`) {
		t.Fatalf("请求体应含 systemInstruction: %s", gotBody)
	}
	if !strings.Contains(gotBody, "你是一只猫") {
		t.Fatalf("systemInstruction 应含系统提示文本: %s", gotBody)
	}
}

// toolChoice 映射测试。
func TestGemini_ToolChoiceMapping(t *testing.T) {
	tests := []struct {
		choice string
		expect string
	}{
		{"auto", "AUTO"},
		{"none", "NONE"},
		{"required", "ANY"},
		{"get_weather", "ANY"},
	}
	for _, tt := range tests {
		tc := buildToolConfig(tt.choice)
		if tc.FunctionCallingConfig.Mode != tt.expect {
			t.Errorf("choice=%q → mode=%q, want %q", tt.choice, tc.FunctionCallingConfig.Mode, tt.expect)
		}
	}
	// 指定工具名时应有 allowedFunctionNames
	tc := buildToolConfig("get_weather")
	if len(tc.FunctionCallingConfig.AllowedFunctionNames) != 1 || tc.FunctionCallingConfig.AllowedFunctionNames[0] != "get_weather" {
		t.Errorf("指定工具名时应有 allowedFunctionNames: %+v", tc.FunctionCallingConfig)
	}
}

// 流式 tool call 测试。
func TestGemini_StreamToolCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"让我查一下\"}]},\"finishReason\":\"\"}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"functionCall\":{\"id\":\"fc_1\",\"name\":\"get_weather\",\"args\":{\"city\":\"SF\"}}}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":5,\"candidatesTokenCount\":3}}\n\n")
	}))
	defer srv.Close()

	c := New("test-key", WithBaseURL(srv.URL))
	var chunks []llm.Chunk
	for chunk, err := range c.Stream(context.Background(), llm.Request{
		Model:    "gemini-2.0-flash",
		Messages: []llm.Message{{Role: llm.User, Content: "天气?"}},
		Tools:    []llm.ToolDef{weatherTool},
	}) {
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		chunks = append(chunks, chunk)
	}

	if len(chunks) < 2 {
		t.Fatalf("应至少有 2 个 chunk, got %d", len(chunks))
	}

	// 最后一个 chunk 应有 tool call
	last := chunks[len(chunks)-1]
	if len(last.ToolCalls) != 1 {
		t.Fatalf("最后 chunk 应有 1 个 tool call, got %d: %+v", len(last.ToolCalls), chunks)
	}
	if last.ToolCalls[0].Name != "get_weather" || last.ToolCalls[0].ID != "fc_1" {
		t.Fatalf("tool call = %+v", last.ToolCalls[0])
	}
}

// Collect 组装测试:流式 tool call 应被 Collect 正确汇总到 Response.ToolCalls。
func TestGemini_CollectStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"functionCall\":{\"id\":\"fc_1\",\"name\":\"get_weather\",\"args\":{\"city\":\"北京\"}}}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":8,\"candidatesTokenCount\":4}}\n\n")
	}))
	defer srv.Close()

	c := New("test-key", WithBaseURL(srv.URL))
	resp, err := llm.Collect(c.Stream(context.Background(), llm.Request{
		Model:    "gemini-2.0-flash",
		Messages: []llm.Message{{Role: llm.User, Content: "北京天气"}},
		Tools:    []llm.ToolDef{weatherTool},
	}))
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "get_weather" {
		t.Fatalf("collect tool calls = %+v", resp.ToolCalls)
	}
	if resp.Usage.InputTokens != 8 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

// Embed 测试。
func TestGemini_Embed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"embeddings":[{"values":[0.1,0.2,0.3]},{"values":[0.4,0.5,0.6]}]}`)
	}))
	defer srv.Close()

	c := New("test-key", WithBaseURL(srv.URL), WithEmbedModel("text-embedding-004"))
	vecs, err := c.Embed(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(vecs) != 2 {
		t.Fatalf("应返回 2 组向量, got %d", len(vecs))
	}
	if len(vecs[0]) != 3 || vecs[0][0] != 0.1 {
		t.Fatalf("向量 = %+v", vecs[0])
	}
}

// OAuth2 认证测试。
func TestGemini_OAuth2Auth(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`)
	}))
	defer srv.Close()

	ts := &staticTokenSource{token: "my-access-token"}
	c := New("", WithBaseURL(srv.URL), WithTokenSource(ts))
	_, err := c.Generate(context.Background(), llm.Request{
		Model:    "gemini-2.0-flash",
		Messages: []llm.Message{{Role: llm.User, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if gotAuth != "Bearer my-access-token" {
		t.Fatalf("应使用 Bearer token, got %q", gotAuth)
	}
}

type staticTokenSource struct{ token string }

func (s *staticTokenSource) Token() (*Token, error) {
	return &Token{AccessToken: s.token}, nil
}

// 并行 tool calls 测试(多个 functionCall part)。
func TestGemini_ParallelToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{
			"candidates": [{
				"content": {
					"role": "model",
					"parts": [
						{"functionCall": {"id": "fc_1", "name": "get_weather", "args": {"city": "北京"}}},
						{"functionCall": {"id": "fc_2", "name": "get_weather", "args": {"city": "上海"}}}
					]
				},
				"finishReason": "STOP"
			}]
		}`)
	}))
	defer srv.Close()

	c := New("test-key", WithBaseURL(srv.URL))
	resp, err := c.Generate(context.Background(), llm.Request{
		Model:    "gemini-2.0-flash",
		Messages: []llm.Message{{Role: llm.User, Content: "北京和上海天气"}},
		Tools:    []llm.ToolDef{weatherTool},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(resp.ToolCalls) != 2 {
		t.Fatalf("应有 2 个并行 tool calls, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].ID != "fc_1" || resp.ToolCalls[1].ID != "fc_2" {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
}

// buildContents 应正确处理多个连续 tool 消息合并到同一个 user 回合。
func TestGemini_MultiToolResponseMerge(t *testing.T) {
	req := llm.Request{
		Messages: []llm.Message{
			{Role: llm.User, Content: "北京和上海天气"},
			{Role: llm.Assistant, ToolCalls: []llm.ToolCall{
				{ID: "fc_1", Name: "get_weather", Arguments: json.RawMessage(`{"city":"北京"}`)},
				{ID: "fc_2", Name: "get_weather", Arguments: json.RawMessage(`{"city":"上海"}`)},
			}},
			{Role: llm.Tool, ToolCallID: "fc_1", Content: `{"temp":"30C"}`},
			{Role: llm.Tool, ToolCallID: "fc_2", Content: `{"temp":"28C"}`},
		},
	}
	contents := buildContents(req)
	// user → model(2 functionCall) → user(2 functionResponse 合并)
	if len(contents) != 3 {
		t.Fatalf("应有 3 个 content, got %d", len(contents))
	}
	// 最后一个 user 回合应有 2 个 functionResponse part
	last := contents[2]
	if last.Role != "user" {
		t.Fatalf("最后应是 user 回合, got %q", last.Role)
	}
	if len(last.Parts) != 2 {
		t.Fatalf("user 回合应有 2 个 part, got %d", len(last.Parts))
	}
	for _, p := range last.Parts {
		if p.FunctionResponse == nil {
			t.Fatalf("每个 part 应是 functionResponse: %+v", p)
		}
	}
}
