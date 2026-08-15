package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strings"
	"sync"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

// ---- 测试用 stub client ----

type exScript struct {
	outs []string
	i    int
}

func (c *exScript) Generate(_ context.Context, _ llm.Request) (*llm.Response, error) {
	out := c.outs[c.i%len(c.outs)]
	c.i++
	return &llm.Response{Content: out}, nil
}

func (c *exScript) Stream(context.Context, llm.Request) iter.Seq2[llm.Chunk, error] {
	return unusedStream()
}

type exConst struct{ content string }

func (c exConst) Generate(context.Context, llm.Request) (*llm.Response, error) {
	return &llm.Response{Content: c.content}, nil
}

func (c exConst) Stream(context.Context, llm.Request) iter.Seq2[llm.Chunk, error] {
	return unusedStream()
}

type exPool struct {
	mu   sync.Mutex
	outs []string
	i    int
}

func (c *exPool) Generate(context.Context, llm.Request) (*llm.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.outs[c.i%len(c.outs)]
	c.i++
	return &llm.Response{Content: out}, nil
}

func (c *exPool) Stream(context.Context, llm.Request) iter.Seq2[llm.Chunk, error] {
	return unusedStream()
}

// exToolClient 支持一次 tool_call 然后回复的 stub。
type exToolClient struct {
	toolResp string
	finalOut string
	called   bool
}

func (c *exToolClient) Generate(_ context.Context, req llm.Request) (*llm.Response, error) {
	if !c.called && len(req.Tools) > 0 {
		c.called = true
		return &llm.Response{
			Content: "",
			ToolCalls: []llm.ToolCall{{
				ID: "tc_1", Name: req.Tools[0].Name,
				Arguments: json.RawMessage(`{"city":"北京"}`),
			}},
		}, nil
	}
	return &llm.Response{Content: c.finalOut}, nil
}

func (c *exToolClient) Stream(context.Context, llm.Request) iter.Seq2[llm.Chunk, error] {
	return unusedStream()
}

// ---- 基础示例 ----

// ExampleRunner_basic 展示最基本的 agent 运行:模型→工具→回复。
func ExampleRunner_basic() {
	r := &agent.Runner{
		Client: &exToolClient{finalOut: "北京今天 25°C,晴。"},
		Tools: []agent.Tool{
			agent.Func("get_weather", "查天气",
				json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
				func(_ context.Context, _ json.RawMessage) (string, error) {
					return `{"temp":25,"cond":"晴"}`, nil
				}),
		},
	}
	out := agent.CollectOutcome(r.Run(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "北京天气?"}},
	}))
	resp, _ := out.Final()
	fmt.Println(resp.Content)
	// Output: 北京今天 25°C,晴。
}

// ExampleRunner_stream 展示流式消费事件流。
func ExampleRunner_stream() {
	r := &agent.Runner{Client: exConst{content: "你好,世界!"}}
	var parts []string
	for ev, err := range r.Run(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "打招呼"}},
	}) {
		if err != nil {
			break
		}
		if ev.Type == agent.EventFinal {
			parts = append(parts, ev.Response.Content)
		}
	}
	fmt.Println(strings.Join(parts, ""))
	// Output: 你好,世界!
}

// ExampleRunner_withOptions 展示 per-run options。
func ExampleRunner_withOptions() {
	cli := &exScript{outs: []string{"模型已切换"}}
	r := &agent.Runner{Client: cli}
	out := agent.CollectOutcome(r.Run(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "test"}},
	}, agent.WithModel("gpt-4o"), agent.WithMaxSteps(16)))
	resp, _ := out.Final()
	fmt.Println(resp.Content)
	// Output: 模型已切换
}

// ---- Agent 级中间件 ----

// ExampleRunner_middleware 展示 Agent 级中间件(日志 + 来源标记)。
func ExampleRunner_middleware() {
	var logged []string
	logFn := func(_ context.Context, event string, _ map[string]any) {
		logged = append(logged, event)
	}
	r := &agent.Runner{
		Name:   "demo",
		Client: exConst{content: "done"},
		Middlewares: []agent.AgentMiddleware{
			agent.LoggingMiddleware(logFn),
			agent.SourceAttributionMiddleware("demo"),
		},
	}
	// 完整消费事件流(不提前 return),触发 start + done 日志
	var finalContent string
	for ev, err := range r.Run(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "hi"}},
	}) {
		if err != nil {
			break
		}
		if ev.Type == agent.EventFinal {
			finalContent = ev.Response.Content
		}
	}
	fmt.Println(finalContent)
	fmt.Println(strings.Join(logged, ","))
	// Output:
	// done
	// agent.run.start,agent.run.done
}

// ---- History / Context Provider ----

// ExampleRunner_historyProvider 展示 HistoryProvider 自动管理会话历史。
func ExampleRunner_historyProvider() {
	hp := agent.NewInMemoryHistoryProvider()
	cli := &exScript{outs: []string{"首轮回复", "记得你说过 hello"}}
	r := &agent.Runner{
		Client:      cli,
		HistoryProv: hp,
		SessionID:   "s1",
	}

	// 第一轮
	agent.CollectOutcome(r.Run(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "hello"}},
	}))
	// 第二轮:HistoryProvider 自动注入上次对话
	out := agent.CollectOutcome(r.Run(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "你还记得吗?"}},
	}))
	resp, _ := out.Final()
	fmt.Println(resp.Content)
	// Output: 记得你说过 hello
}

// ExampleRunner_contextProvider 展示 ContextProvider 注入 RAG 上下文。
func ExampleRunner_contextProvider() {
	r := &agent.Runner{
		Client: exConst{content: "根据资料,答案是 42。"},
		ContextProvs: []agent.ContextProvider{
			agent.RAGContextProvider(func(_ context.Context, query string) ([]string, error) {
				return []string{"文档 A: 生命的答案是 42。"}, nil
			}),
		},
	}
	out := agent.CollectOutcome(r.Run(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "生命的答案?"}},
	}))
	resp, _ := out.Final()
	fmt.Println(resp.Content)
	// Output: 根据资料,答案是 42。
}

// ---- 编排原语 ----

func ExampleReActPlanner() {
	client := &exScript{outs: []string{
		"/*PLANNING*/ 先拆解问题\n/*REASONING*/ 6 乘 7\nFINAL ANSWER: 42",
	}}
	r := &agent.Runner{Client: client, Planner: &agent.ReActPlanner{}}

	out := agent.CollectOutcome(r.Run(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "6*7=?"}},
	}))
	resp, _ := out.Final()
	fmt.Println(resp.Content)
	// Output: 42
}

func ExampleTeam() {
	researcher := &agent.Runner{Name: "researcher", Client: &exScript{outs: []string{
		"HANDOFF: writer 请把调研结论写成一段报告",
	}}}
	writer := &agent.Runner{Name: "writer", Client: exConst{content: "报告:方案 A 最优。"}}

	team := &agent.Team{
		Members: map[string]agent.Agent{"researcher": researcher, "writer": writer},
		Entry:   "researcher",
	}
	out := agent.CollectOutcome(team.Run(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "调研主题 X"}},
	}))
	resp, _ := out.Final()
	fmt.Println(resp.Content)
	// Output: 报告:方案 A 最优。
}

func ExampleBestOfN() {
	sub := &agent.Runner{Client: &exPool{outs: []string{"短", "中等", "最长的候选答案"}}}
	best := &agent.BestOfN{Agent: sub, N: 3}

	out := agent.CollectOutcome(best.Run(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "给个答复"}},
	}))
	resp, _ := out.Final()
	fmt.Println(resp.Content)
	// Output: 最长的候选答案
}

func ExampleVerifyLoop() {
	client := &exScript{outs: []string{"初稿", "修订稿 OK"}}
	loop := &agent.VerifyLoop{
		Agent: &agent.Runner{Client: client},
		Verify: func(_ context.Context, resp *llm.Response) (bool, string, error) {
			if strings.Contains(resp.Content, "OK") {
				return true, "", nil
			}
			return false, "请在结尾加上 OK 标记", nil
		},
	}
	out := agent.CollectOutcome(loop.Run(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "写点东西"}},
	}))
	resp, _ := out.Final()
	fmt.Println(resp.Content)
	// Output: 修订稿 OK
}

// Example_nesting 展示 BestOfN + VerifyLoop 任意嵌套。
func Example_nesting() {
	inner := &agent.Runner{Client: &exPool{outs: []string{"x", "yy", "zzz OK"}}}
	best := &agent.BestOfN{Agent: inner, N: 3}
	loop := &agent.VerifyLoop{
		Agent: best,
		Verify: func(_ context.Context, resp *llm.Response) (bool, string, error) {
			return strings.Contains(resp.Content, "OK"), "需含 OK", nil
		},
	}
	out := agent.CollectOutcome(loop.Run(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "q"}},
	}))
	resp, _ := out.Final()
	fmt.Println(resp.Content)
	// Output: zzz OK
}

// ---- 人工审批 ----

// ExampleRunner_approval 展示 PermitAsk 暂停 → Continue 续跑。
func ExampleRunner_approval() {
	cli := &exScript{outs: []string{
		`{"tool_call":"approve_action"}`,
		"操作已执行",
	}}
	r := &agent.Runner{
		Client: cli,
		Tools: []agent.Tool{
			{
				Def:        llm.ToolDef{Name: "approve_action", Description: "需审批的操作"},
				Call:       func(_ context.Context, _ json.RawMessage) (string, error) { return "ok", nil },
				Permission: agent.PermitAsk,
			},
		},
	}

	out := agent.CollectOutcome(r.Run(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "执行危险操作"}},
	}))
	if out.IsPaused() {
		resolutions := make([]agent.Resolution, len(out.Requirements))
		for i, rq := range out.Requirements {
			resolutions[i] = agent.Resolution{ID: rq.ID, Approved: true}
		}
		out = agent.CollectOutcome(r.Continue(context.Background(), out.RunID, resolutions))
	}
	fmt.Println(out.Status)
	// Output: done
}
