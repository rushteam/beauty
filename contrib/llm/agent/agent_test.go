package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

// fakeClient 按预设脚本逐次返回响应,并记录每次收到的消息条数,便于断言循环行为。
type fakeClient struct {
	steps    []*llm.Response
	i        int
	lastReq  llm.Request
	genCalls int
}

func (f *fakeClient) Generate(_ context.Context, req llm.Request) (*llm.Response, error) {
	f.lastReq = req
	f.genCalls++
	r := f.steps[f.i]
	f.i++
	return r, nil
}

func (f *fakeClient) Stream(context.Context, llm.Request) (<-chan llm.Chunk, error) {
	return nil, errors.New("unused")
}

func echoTool() agent.Tool {
	return agent.Func("echo", "回显", nil, func(_ context.Context, args json.RawMessage) (string, error) {
		return "echoed:" + string(args), nil
	})
}

// 一轮工具调用后拿到终态文本。
func TestRunner_ToolLoop(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo", Arguments: json.RawMessage(`{"x":1}`)}}},
		{Content: "done"},
	}}
	r := &agent.Runner{Client: fc, Tools: []agent.Tool{echoTool()}}

	resp, err := r.Run(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.User, Content: "go"}}})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if resp.Content != "done" {
		t.Fatalf("final content = %q", resp.Content)
	}
	if fc.genCalls != 2 {
		t.Fatalf("应调用 Generate 2 次, got %d", fc.genCalls)
	}
	// 第二轮请求应包含:user, assistant(tool_calls), tool(result)
	msgs := fc.lastReq.Messages
	if len(msgs) != 3 {
		t.Fatalf("第二轮消息数 = %d, want 3: %+v", len(msgs), msgs)
	}
	if msgs[1].Role != llm.Assistant || len(msgs[1].ToolCalls) != 1 {
		t.Fatalf("第二条应为带 tool_calls 的 assistant: %+v", msgs[1])
	}
	if msgs[2].Role != llm.Tool || msgs[2].ToolCallID != "c1" || msgs[2].Content != `echoed:{"x":1}` {
		t.Fatalf("第三条应为工具结果: %+v", msgs[2])
	}
	// tools 应被注入。
	if len(fc.lastReq.Tools) != 1 || fc.lastReq.Tools[0].Name != "echo" {
		t.Fatalf("tools 未注入: %+v", fc.lastReq.Tools)
	}
}

// 未知工具:错误文本喂回,循环继续而非中断。
func TestRunner_UnknownTool(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "nope"}}},
		{Content: "recovered"},
	}}
	r := &agent.Runner{Client: fc, Tools: []agent.Tool{echoTool()}}
	resp, err := r.Run(context.Background(), llm.Request{Model: "m"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if resp.Content != "recovered" {
		t.Fatalf("content = %q", resp.Content)
	}
	last := fc.lastReq.Messages
	if got := last[len(last)-1].Content; got != `error: unknown tool "nope"` {
		t.Fatalf("未知工具结果 = %q", got)
	}
}

// 工具执行出错:错误文本喂回。
func TestRunner_ToolError(t *testing.T) {
	failing := agent.Func("boom", "", nil, func(context.Context, json.RawMessage) (string, error) {
		return "", errors.New("kaboom")
	})
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "boom"}}},
		{Content: "ok"},
	}}
	r := &agent.Runner{Client: fc, Tools: []agent.Tool{failing}}
	if _, err := r.Run(context.Background(), llm.Request{Model: "m"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	last := fc.lastReq.Messages
	if got := last[len(last)-1].Content; got != "error: kaboom" {
		t.Fatalf("工具错误结果 = %q", got)
	}
}

// 一直要求调用工具 → 到 MaxSteps 返回 ErrMaxSteps + 最后响应。
func TestRunner_MaxSteps(t *testing.T) {
	loop := &llm.Response{ToolCalls: []llm.ToolCall{{ID: "c", Name: "echo"}}}
	fc := &fakeClient{steps: []*llm.Response{loop, loop, loop}}
	r := &agent.Runner{Client: fc, Tools: []agent.Tool{echoTool()}, MaxSteps: 3}
	resp, err := r.Run(context.Background(), llm.Request{Model: "m"})
	if !errors.Is(err, agent.ErrMaxSteps) {
		t.Fatalf("应返回 ErrMaxSteps, got %v", err)
	}
	if resp == nil {
		t.Fatal("应同时返回最后一次响应")
	}
	if fc.genCalls != 3 {
		t.Fatalf("应恰好 3 次 Generate, got %d", fc.genCalls)
	}
}

// 无工具调用时直接返回,不改动调用方的消息切片。
func TestRunner_NoToolAndImmutability(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{{Content: "hi"}}}
	r := &agent.Runner{Client: fc}
	in := []llm.Message{{Role: llm.User, Content: "yo"}}
	resp, err := r.Run(context.Background(), llm.Request{Model: "m", Messages: in})
	if err != nil || resp.Content != "hi" {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
	if len(in) != 1 {
		t.Fatalf("调用方消息切片被改动: %+v", in)
	}
}
