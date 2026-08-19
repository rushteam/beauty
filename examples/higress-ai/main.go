// Command higress-ai 演示 beauty + Higress AI 网关的两个核心集成:
//
//  1. LLM 请求经 Higress AI 网关代理(统一模型路由/限流/降级)
//  2. MCP Server 经 Higress 暴露给外部 AI 客户端(认证/限流由网关负责)
//
// 默认用离线 stub 模型(无需 API key);设置 HIGRESS_AI_ENDPOINT 后 LLM 请求走网关。
//
// 运行:
//
//	cd examples/higress-ai && go run .
//	# 或指定 Higress:
//	HIGRESS_AI_ENDPOINT=http://higress-gw:80/v1 HIGRESS_AI_TOKEN=xxx go run .
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
	"github.com/rushteam/beauty/contrib/llm/openai"
	"github.com/rushteam/beauty/contrib/mcp"
	"github.com/rushteam/beauty/pkg/service/webserver"
	"github.com/rushteam/beauty/pkg/transport/sse"
)

func main() {
	addr := envOr("ADDR", ":8080")
	model := envOr("MODEL", "gpt-4o-mini")

	// ═══════════════════════════════════════════════════════════════════
	// 1) LLM Client: 优先走 Higress AI 网关,降级直连
	// ═══════════════════════════════════════════════════════════════════
	llmClient := buildLLMClient(model)

	// ═══════════════════════════════════════════════════════════════════
	// 2) MCP Server: 暴露工具供外部 AI Client 调用(经 Higress 认证后到达)
	// ═══════════════════════════════════════════════════════════════════
	mcpSrv := buildMCPServer()

	// ═══════════════════════════════════════════════════════════════════
	// 3) Agent Runner: 内部消费 LLM + 工具编排
	// ═══════════════════════════════════════════════════════════════════
	runner := &agent.Runner{
		Client: llmClient,
		Tools: []agent.Tool{
			agent.Func("now", "返回当前服务器时间", nil,
				func(_ context.Context, _ json.RawMessage) (string, error) {
					return time.Now().Format(time.RFC3339), nil
				}),
			agent.Func("calc", "简单计算器:加法", json.RawMessage(`{"type":"object","properties":{"a":{"type":"number"},"b":{"type":"number"}},"required":["a","b"]}`),
				func(_ context.Context, args json.RawMessage) (string, error) {
					var in struct{ A, B float64 }
					json.Unmarshal(args, &in)
					return fmt.Sprintf("%.2f", in.A+in.B), nil
				}),
		},
	}

	// ═══════════════════════════════════════════════════════════════════
	// 4) HTTP 路由
	// ═══════════════════════════════════════════════════════════════════
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.HTTPHandler(mcpSrv)) // MCP 端点(Higress 暴露)
	mux.HandleFunc("POST /chat", chatHandler(runner, model))
	mux.HandleFunc("GET /stream", sse.Handler(streamHandler(runner, model)))
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /{$}", usage)

	fmt.Printf("higress-ai 监听 %s (model=%s, higress=%s)\n",
		addr, model, envOr("HIGRESS_AI_ENDPOINT", "<直连/stub>"))

	app := beauty.New(
		beauty.WithWebServer(addr, mux, webserver.WithServiceName("higress-ai")),
	)
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}

// buildLLMClient 构建 LLM 客户端,优先走 Higress,降级到直连或 stub。
func buildLLMClient(model string) llm.Client {
	var clients []llm.Client

	// 优先: Higress AI 网关
	if ep := os.Getenv("HIGRESS_AI_ENDPOINT"); ep != "" {
		token := envOr("HIGRESS_AI_TOKEN", "")
		hc := openai.New(token, openai.WithBaseURL(ep))
		clients = append(clients, wrapWithMetrics(hc, "higress"))
		slog.Info("LLM: Higress AI 网关已配置", "endpoint", ep)
	}

	// 降级: 直连 OpenAI
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		var opts []openai.Option
		if u := os.Getenv("OPENAI_BASE_URL"); u != "" {
			opts = append(opts, openai.WithBaseURL(u))
		}
		dc := openai.New(key, opts...)
		clients = append(clients, wrapWithMetrics(dc, "direct"))
		slog.Info("LLM: 直连 OpenAI 已配置")
	}

	// 兜底: 离线 stub
	if len(clients) == 0 {
		slog.Info("LLM: 无 API 配置,使用离线 stub")
		return &stubClient{model: model}
	}

	if len(clients) == 1 {
		return clients[0]
	}
	return llm.Fallback(clients...)
}

func wrapWithMetrics(c llm.Client, source string) llm.Client {
	return llm.Metered(c, func(_ context.Context, model string, u llm.Usage, d time.Duration) {
		slog.Info("LLM usage",
			"source", source, "model", model,
			"input", u.InputTokens, "output", u.OutputTokens,
			"latency", d.Round(time.Millisecond))
	})
}

// buildMCPServer 创建 MCP Server 并注册示例工具。
func buildMCPServer() *mcp.Server {
	srv := mcp.NewServer("beauty-higress-demo", "1.0.0")

	type AddIn struct {
		A int `json:"a" jsonschema:"第一个加数"`
		B int `json:"b" jsonschema:"第二个加数"`
	}
	type AddOut struct {
		Sum int `json:"sum"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "add", Description: "两数相加"},
		func(_ context.Context, _ *mcp.CallToolRequest, in AddIn) (*mcp.CallToolResult, AddOut, error) {
			sum := in.A + in.B
			return mcp.Text(strconv.Itoa(sum)), AddOut{Sum: sum}, nil
		})

	type TimeIn struct{}
	type TimeOut struct {
		Time string `json:"time"`
	}
	mcp.AddTool(srv, &mcp.Tool{Name: "server_time", Description: "获取服务器当前时间"},
		func(_ context.Context, _ *mcp.CallToolRequest, _ TimeIn) (*mcp.CallToolResult, TimeOut, error) {
			now := time.Now().Format(time.RFC3339)
			return mcp.Text(now), TimeOut{Time: now}, nil
		})

	return srv
}

// ═══════════════ HTTP Handlers ═══════════════

func chatHandler(runner *agent.Runner, model string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Message string `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Message == "" {
			http.Error(w, `需要 JSON body {"message":"..."}`, http.StatusBadRequest)
			return
		}
		outcome := agent.CollectOutcome(runner.Run(r.Context(), llm.Request{
			Model:    model,
			Messages: []llm.Message{{Role: llm.User, Content: in.Message}},
		}))
		if outcome.Err != nil {
			http.Error(w, outcome.Err.Error(), http.StatusInternalServerError)
			return
		}
		answer := ""
		if outcome.Response != nil {
			answer = outcome.Response.Content
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"answer": answer})
	}
}

func streamHandler(runner *agent.Runner, model string) func(*http.Request, sse.Sink) error {
	return func(r *http.Request, sink sse.Sink) error {
		q := r.URL.Query().Get("q")
		if q == "" {
			return sink.Send(sse.Event{Event: "error", Data: "缺少 ?q= 参数"})
		}
		for ev, err := range runner.Run(r.Context(), llm.Request{
			Model:    model,
			Messages: []llm.Message{{Role: llm.User, Content: q}},
		}) {
			if err != nil {
				sink.Send(sse.Event{Event: "error", Data: err.Error()})
				return nil
			}
			switch ev.Type {
			case agent.EventToolStart:
				if ev.ToolCall != nil {
					sink.Send(sse.Event{Event: "tool", Data: fmt.Sprintf("%s(%s)", ev.ToolCall.Name, string(ev.ToolCall.Arguments))})
				}
			case agent.EventFinal:
				content := ""
				if ev.Response != nil {
					content = ev.Response.Content
				}
				sink.Send(sse.Event{Event: "answer", Data: content})
			case agent.EventError:
				msg := "unknown error"
				if ev.Err != nil {
					msg = ev.Err.Error()
				}
				sink.Send(sse.Event{Event: "error", Data: msg})
			}
		}
		return nil
	}
}

func usage(w http.ResponseWriter, _ *http.Request) {
	w.Write([]byte(`higress-ai —— Higress AI 网关 × Beauty 集成示例

端点:
  GET  /          本页
  POST /chat      body {"message":"..."}  → {"answer":"..."}
  GET  /stream    ?q=问题              → SSE: tool / answer 事件
  ANY  /mcp       MCP Streamable HTTP 端点(外部 AI Client 调用)
  GET  /health    健康检查

环境变量:
  HIGRESS_AI_ENDPOINT  Higress AI 网关(如 http://higress-gw/v1), 不设则直连/stub
  HIGRESS_AI_TOKEN     网关 consumer 凭证
  OPENAI_API_KEY       直连 OpenAI(降级用)
  OPENAI_BASE_URL      覆盖 OpenAI 地址(可选)
  MODEL                模型名(默认 gpt-4o-mini)
  ADDR                 监听地址(默认 :8080)

示例:
  curl -s localhost:8080/chat -d '{"message":"现在几点"}'
  curl -N 'localhost:8080/stream?q=1加2等于几'
`))
}

// ═══════════════ 离线 Stub ═══════════════

type stubClient struct{ model string }

func (s *stubClient) Generate(_ context.Context, req llm.Request) (*llm.Response, error) {
	msgs := req.Messages
	if n := len(msgs); n > 0 && msgs[n-1].Role == llm.Tool {
		return &llm.Response{Model: s.model, Content: "(stub) 工具结果: " + msgs[n-1].Content}, nil
	}
	u := lastUserMsg(msgs)
	switch {
	case contains(u, "时间", "几点", "time"):
		return &llm.Response{Model: s.model, StopReason: "tool_calls",
			ToolCalls: []llm.ToolCall{{ID: "c1", Name: "now", Arguments: json.RawMessage(`{}`)}}}, nil
	case contains(u, "加", "calc", "+", "计算"):
		return &llm.Response{Model: s.model, StopReason: "tool_calls",
			ToolCalls: []llm.ToolCall{{ID: "c1", Name: "calc", Arguments: json.RawMessage(`{"a":1,"b":2}`)}}}, nil
	default:
		return &llm.Response{Model: s.model, Content: fmt.Sprintf("(stub) 你说:「%s」。设 HIGRESS_AI_ENDPOINT 或 OPENAI_API_KEY 启用真实模型。", u)}, nil
	}
}

func (s *stubClient) Stream(ctx context.Context, req llm.Request) iter.Seq2[llm.Chunk, error] {
	return func(yield func(llm.Chunk, error) bool) {
		resp, err := s.Generate(ctx, req)
		if err != nil {
			yield(llm.Chunk{}, err)
			return
		}
		chunk := llm.Chunk{Delta: resp.Content}
		if len(resp.ToolCalls) > 0 {
			chunk.ToolCalls = resp.ToolCalls
		}
		yield(chunk, nil)
	}
}

// ═══════════════ Helpers ═══════════════

func lastUserMsg(msgs []llm.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.User {
			return msgs[i].Content
		}
	}
	return ""
}

func contains(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && containsStr(s, sub) {
			return true
		}
	}
	return false
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && stringContains(s, sub))
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
