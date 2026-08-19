// Command agentservice 把整条 agent 链路跑成一个 beauty 服务:
//
//	Guard 护栏 → agent.Runner 工具循环(skills 工具 + now 工具 + PermitAsk delete 工具)
//	           → session.Manager 多轮记忆 + 滚动摘要
//	通过 beauty.WithWebServer 暴露:POST /chat(非流式)与 GET /stream(SSE 逐步事件)。
//
// 默认用离线 stub 模型(无需 API key 即可跑通全链路);设置 OPENAI_API_KEY 则切换到真实 OpenAI
// (可用 OPENAI_BASE_URL / MODEL 覆盖)。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/service/webserver"
	"github.com/rushteam/beauty/pkg/sse"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
	"github.com/rushteam/beauty/contrib/llm/agent/session"
	"github.com/rushteam/beauty/contrib/llm/agent/skills"
	"github.com/rushteam/beauty/contrib/llm/openai"
)

func main() {
	model := envOr("MODEL", "gpt-4o-mini")

	var base llm.Client
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		opts := []openai.Option{}
		if u := os.Getenv("OPENAI_BASE_URL"); u != "" {
			opts = append(opts, openai.WithBaseURL(u))
		}
		base = openai.New(key, opts...)
	} else {
		base = &demoClient{}
	}
	client := llm.Guard(base,
		llm.PromptInjection(),
		llm.PII(),
		llm.MaxInputLen(4000),
	)

	sk, err := skills.Load(skills.LocalSkills{Dir: "skills"})
	if err != nil {
		panic(err)
	}
	systemPrompt := joinLines(
		"你是一个可调用工具的助手。需要时调用工具;涉及删除等敏感操作会经人工审批。",
		sk.SystemPrompt(),
	)

	tools := append([]agent.Tool{nowTool(), deleteTool()}, sk.Tools()...)

	mgr := &session.Manager{
		Store:      session.NewMemoryStore(),
		Summarizer: &session.Summarizer{Client: client, Model: model, MaxMessages: 12, KeepRecent: 4},
	}

	deps := &server{client: client, tools: tools, model: model, system: systemPrompt, mgr: mgr}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", usage)
	mux.HandleFunc("POST /chat", deps.chat)
	mux.HandleFunc("GET /stream", sse.Handler(deps.stream))

	addr := envOr("ADDR", ":8080")
	fmt.Printf("agentservice 监听 %s(model=%s,真实模型=%v)\n", addr, model, os.Getenv("OPENAI_API_KEY") != "")
	app := beauty.New(
		beauty.WithWebServer(addr, mux, webserver.WithServiceName("agentservice")),
	)
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}

type server struct {
	client llm.Client
	tools  []agent.Tool
	model  string
	system string
	mgr    *session.Manager
}

func (s *server) buildRunner(sink sse.Sink) *agent.Runner {
	r := &agent.Runner{
		Client: s.client,
		Tools:  s.tools,
	}
	if sink != nil {
		r.Hooks = agent.Hooks{
			AfterModel: func(_ context.Context, step int, resp *llm.Response) error {
				for _, tc := range resp.ToolCalls {
					_ = sink.Send(sse.Event{Event: "tool", Data: fmt.Sprintf("step %d: 调用 %s %s", step, tc.Name, string(tc.Arguments))})
				}
				return nil
			},
		}
	}
	return r
}

func (s *server) request(msg string) llm.Request {
	return llm.Request{Model: s.model, System: s.system, Messages: []llm.Message{{Role: llm.User, Content: msg}}}
}

func (s *server) chat(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Session string `json:"session"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Message == "" {
		http.Error(w, "需要 JSON body {session, message}", http.StatusBadRequest)
		return
	}
	if in.Session == "" {
		in.Session = "default"
	}
	out := s.mgr.Run(r.Context(), in.Session, s.buildRunner(nil), s.request(in.Message))
	resp, err := out.Final()
	if err != nil {
		var ge *llm.GuardError
		if errors.As(err, &ge) {
			http.Error(w, "被护栏拦截: "+ge.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"answer": resp.Content})
}

func (s *server) stream(r *http.Request, sink sse.Sink) error {
	q := r.URL.Query()
	sessionID := orDefault(q.Get("session"), "default")
	msg := q.Get("q")
	if msg == "" {
		return sink.Send(sse.Event{Event: "error", Data: "缺少查询参数 q"})
	}
	_ = sink.Send(sse.Event{Event: "start", Data: "session=" + sessionID})

	out := s.mgr.Run(r.Context(), sessionID, s.buildRunner(sink), s.request(msg))
	resp, err := out.Final()
	if err != nil {
		return sink.Send(sse.Event{Event: "error", Data: err.Error()})
	}
	return sink.Send(sse.Event{Event: "answer", Data: resp.Content})
}

// ---- 工具 ----

func nowTool() agent.Tool {
	return agent.Func("now", "返回当前服务器时间(RFC3339)", nil,
		func(_ context.Context, _ json.RawMessage) (string, error) {
			return time.Now().Format(time.RFC3339), nil
		})
}

func deleteTool() agent.Tool {
	t := agent.Func("delete_file", "删除指定路径的文件(敏感操作,需审批)",
		json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
		func(_ context.Context, args json.RawMessage) (string, error) {
			var a struct {
				Path string `json:"path"`
			}
			_ = json.Unmarshal(args, &a)
			return "已删除 " + a.Path, nil
		})
	t.Permission = agent.PermitAsk
	return t
}

// ---- 离线 stub 模型 ----

type demoClient struct{}

func (d *demoClient) Generate(_ context.Context, req llm.Request) (*llm.Response, error) {
	msgs := req.Messages
	if n := len(msgs); n > 0 && msgs[n-1].Role == llm.Tool {
		return &llm.Response{Model: req.Model, Content: "(demo) 工具结果:" + msgs[n-1].Content}, nil
	}
	u := lastUser(msgs)
	switch {
	case containsAny(u, "删除", "delete"):
		path := "/tmp/report.pdf"
		if containsAny(u, "系统", "etc", "/etc") {
			path = "/etc/passwd"
		}
		return toolCall("delete_file", fmt.Sprintf(`{"path":%q}`, path)), nil
	case containsAny(u, "时间", "几点", "time"):
		return toolCall("now", `{}`), nil
	case containsAny(u, "技能", "问候", "skill", "hello"):
		return toolCall("get_skill_instructions", `{"skill_name":"greeter"}`), nil
	default:
		return &llm.Response{Model: req.Model, Content: "(demo) 你说:「" + u + "」。设置 OPENAI_API_KEY 可启用真实模型。试试问「现在几点」「删除文件」「用问候技能」。"}, nil
	}
}

func (d *demoClient) Stream(_ context.Context, _ llm.Request) iter.Seq2[llm.Chunk, error] {
	return func(yield func(llm.Chunk, error) bool) {
		yield(llm.Chunk{}, errors.New("demo client 不支持流式"))
	}
}

func toolCall(name, args string) *llm.Response {
	return &llm.Response{
		StopReason: "tool_calls",
		ToolCalls:  []llm.ToolCall{{ID: "call-1", Name: name, Arguments: json.RawMessage(args)}},
	}
}

// ---- 杂项 ----

func usage(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte(`agentservice —— agent 链路演示

POST /chat     body {"session":"s1","message":"现在几点"}   非流式,返回 {"answer":...}
GET  /stream   ?session=s1&q=删除文件                        SSE:start/tool/approval/answer

示例(离线 stub 默认可跑):
  curl -s localhost:8080/chat -d '{"session":"s1","message":"现在几点"}'
  curl -s localhost:8080/chat -d '{"session":"s1","message":"删除文件"}'
  curl -N 'localhost:8080/stream?session=s1&q=删除文件'

设 OPENAI_API_KEY 切换真实模型;链路:Guard 护栏 → Runner(skills+now+PermitAsk delete)→ session 记忆。
`))
}

func lastUser(msgs []llm.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.User {
			return msgs[i].Content
		}
	}
	return ""
}

func containsAny(s string, subs ...string) bool {
	s = strings.ToLower(s)
	for _, sub := range subs {
		if strings.Contains(s, strings.ToLower(sub)) {
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

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func joinLines(xs ...string) string { return strings.Join(xs, "\n\n") }
