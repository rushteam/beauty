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
	t.Permission = agent.PermitAsk
	return t
}

func lastToolMsg(msgs []llm.Message) llm.Message { return msgs[len(msgs)-1] }

func TestPause_ContinueApproved(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo", Arguments: json.RawMessage(`{"x":1}`)}}},
		{Content: "done"},
	}}
	r := &agent.Runner{Client: fc, Tools: []agent.Tool{gatedEcho()}}
	out := agent.CollectOutcome(r.Run(context.Background(), llm.Request{Model: "m"}))
	if !out.IsPaused() || len(out.Requirements) != 1 {
		t.Fatalf("want paused with 1 req, got %+v", out)
	}
	if fc.genCalls != 1 {
		t.Fatalf("pause 前不应继续 Generate, genCalls=%d", fc.genCalls)
	}
	out = agent.CollectOutcome(r.Continue(context.Background(), out.RunID, []agent.Resolution{{ID: "c1", Approved: true}}))
	resp, err := out.Final()
	if err != nil || resp.Content != "done" {
		t.Fatalf("resp=%+v err=%v status=%s", resp, err, out.Status)
	}
	if got := lastToolMsg(fc.lastReq.Messages).Content; got != `echoed:{"x":1}` {
		t.Fatalf("批准后应执行工具, got %q", got)
	}
}

func TestPause_ContinueDenied(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo"}}},
		{Content: "ok, 换个方式"},
	}}
	r := &agent.Runner{Client: fc, Tools: []agent.Tool{gatedEcho()}}
	out := agent.CollectOutcome(r.Run(context.Background(), llm.Request{Model: "m"}))
	if !out.IsPaused() {
		t.Fatalf("want paused, got %s", out.Status)
	}
	out = agent.CollectOutcome(r.Continue(context.Background(), out.RunID, []agent.Resolution{
		{ID: out.Requirements[0].ID, Approved: false, Reason: "太危险"},
	}))
	resp, err := out.Final()
	if err != nil {
		t.Fatalf("拒绝不应中止: %v", err)
	}
	if resp.Content != "ok, 换个方式" {
		t.Fatalf("content = %q", resp.Content)
	}
	got := lastToolMsg(fc.lastReq.Messages).Content
	if !strings.Contains(got, "被拒绝") || !strings.Contains(got, "太危险") {
		t.Fatalf("应把拒绝理由喂回: %q", got)
	}
}

func TestSyncHITL_Approve(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo", Arguments: json.RawMessage(`{"x":1}`)}}},
		{Content: "done"},
	}}
	called := false
	inner := &agent.Runner{Client: fc, Tools: []agent.Tool{gatedEcho()}}
	r := agent.SyncHITL(inner, func(context.Context, llm.ToolCall) (agent.Resolution, error) {
		called = true
		return agent.Resolution{Approved: true}, nil
	})
	out := agent.CollectOutcome(r.Run(context.Background(), llm.Request{Model: "m"}))
	resp, err := out.Final()
	if err != nil || resp.Content != "done" {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
	if !called {
		t.Fatal("SyncHITL 应调用 approve")
	}
}

func TestSyncHITL_ApproveError(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo"}}},
		{Content: "unreached"},
	}}
	wantErr := errors.New("审批超时")
	inner := &agent.Runner{Client: fc, Tools: []agent.Tool{gatedEcho()}}
	r := agent.SyncHITL(inner, func(context.Context, llm.ToolCall) (agent.Resolution, error) {
		return agent.Resolution{}, wantErr
	})
	out := agent.CollectOutcome(r.Run(context.Background(), llm.Request{Model: "m"}))
	if out.Status != agent.StatusError || !errors.Is(out.Err, wantErr) {
		t.Fatalf("want error %v, got status=%s err=%v", wantErr, out.Status, out.Err)
	}
	if fc.genCalls != 1 {
		t.Fatalf("应在第一次工具调用处中止, genCalls=%d", fc.genCalls)
	}
}

func TestPause_NotGated(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo"}}},
		{Content: "done"},
	}}
	r := &agent.Runner{Client: fc, Tools: []agent.Tool{echoTool()}}
	out := agent.CollectOutcome(r.Run(context.Background(), llm.Request{Model: "m"}))
	if _, err := out.Final(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if out.IsPaused() {
		t.Fatal("未标 PermitAsk 不应 pause")
	}
}

func TestPause_AtomicRound(t *testing.T) {
	// 同轮 Ask+Allow:Allow 也不应在 pause 前执行。
	allowCalled := false
	ask := gatedEcho()
	allow := agent.Func("note", "", nil, func(context.Context, json.RawMessage) (string, error) {
		allowCalled = true
		return "noted", nil
	})
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{
			{ID: "a1", Name: "echo"},
			{ID: "a2", Name: "note"},
		}},
		{Content: "done"},
	}}
	r := &agent.Runner{Client: fc, Tools: []agent.Tool{ask, allow}, ParallelTools: agent.Bool(false)}
	out := agent.CollectOutcome(r.Run(context.Background(), llm.Request{Model: "m"}))
	if !out.IsPaused() {
		t.Fatalf("want paused, got %s", out.Status)
	}
	if allowCalled {
		t.Fatal("原子暂停:Allow 工具也不应在 Continue 前执行")
	}
	out = agent.CollectOutcome(r.Continue(context.Background(), out.RunID, []agent.Resolution{{ID: "a1", Approved: true}}))
	if _, err := out.Final(); err != nil {
		t.Fatal(err)
	}
	if !allowCalled {
		t.Fatal("Continue 后应执行 Allow 工具")
	}
}
