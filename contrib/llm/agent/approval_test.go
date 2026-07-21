package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

func gatedEcho() agent.Tool {
	t := echoTool()
	t.Approval = true
	return t
}

func lastToolMsg(msgs []llm.Message) llm.Message { return msgs[len(msgs)-1] }

// 批准 → 工具执行,结果喂回。
func TestApproval_Approved(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo", Arguments: json.RawMessage(`{"x":1}`)}}},
		{Content: "done"},
	}}
	called := false
	r := &agent.Runner{Client: fc, Tools: []agent.Tool{gatedEcho()},
		Approve: func(context.Context, llm.ToolCall) (agent.Decision, error) {
			called = true
			return agent.Decision{Approved: true}, nil
		}}
	resp, err := r.Run(context.Background(), llm.Request{Model: "m"})
	if err != nil || resp.Content != "done" {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
	if !called {
		t.Fatal("Approve 应被调用")
	}
	if got := lastToolMsg(fc.lastReq.Messages).Content; got != `echoed:{"x":1}` {
		t.Fatalf("批准后应执行工具, got %q", got)
	}
}

// 拒绝 → 拒绝理由喂回模型,工具不执行,循环继续。
func TestApproval_Denied(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo"}}},
		{Content: "ok, 换个方式"},
	}}
	r := &agent.Runner{Client: fc, Tools: []agent.Tool{gatedEcho()},
		Approve: func(context.Context, llm.ToolCall) (agent.Decision, error) {
			return agent.Decision{Approved: false, Reason: "太危险"}, nil
		}}
	resp, err := r.Run(context.Background(), llm.Request{Model: "m"})
	if err != nil {
		t.Fatalf("拒绝不应中止 run: %v", err)
	}
	if resp.Content != "ok, 换个方式" {
		t.Fatalf("content = %q", resp.Content)
	}
	got := lastToolMsg(fc.lastReq.Messages).Content
	if !strings.Contains(got, "被拒绝") || !strings.Contains(got, "太危险") {
		t.Fatalf("应把拒绝理由喂回: %q", got)
	}
}

// 审批门本身出错 → 中止 Run。
func TestApproval_Error(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo"}}},
		{Content: "unreached"},
	}}
	wantErr := errors.New("审批超时")
	r := &agent.Runner{Client: fc, Tools: []agent.Tool{gatedEcho()},
		Approve: func(context.Context, llm.ToolCall) (agent.Decision, error) {
			return agent.Decision{}, wantErr
		}}
	_, err := r.Run(context.Background(), llm.Request{Model: "m"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("审批出错应中止并冒泡, got %v", err)
	}
	if fc.genCalls != 1 {
		t.Fatalf("应在第一次工具调用处中止, genCalls=%d", fc.genCalls)
	}
}

// 工具未标 Approval → 不触发审批门。
func TestApproval_NotGated(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo"}}},
		{Content: "done"},
	}}
	called := false
	r := &agent.Runner{Client: fc, Tools: []agent.Tool{echoTool()}, // Approval=false
		Approve: func(context.Context, llm.ToolCall) (agent.Decision, error) {
			called = true
			return agent.Decision{Approved: true}, nil
		}}
	if _, err := r.Run(context.Background(), llm.Request{Model: "m"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if called {
		t.Fatal("未标 Approval 的工具不应触发审批门")
	}
}
