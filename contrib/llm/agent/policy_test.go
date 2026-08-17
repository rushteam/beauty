package agent_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

func TestParseRule(t *testing.T) {
	got := agent.ParseRule("Bash(npm:*)")
	if got.ToolName != "bash" || got.Pattern != "npm:*" {
		t.Fatalf("got %+v", got)
	}
	got = agent.ParseRule("Read")
	if got.ToolName != "read" || got.Pattern != "" {
		t.Fatalf("got %+v", got)
	}
}

func TestToolPolicy_DenyWins(t *testing.T) {
	p := &agent.ToolPolicy{
		Allow: agent.ParseRules("bash"),
		Deny:  agent.ParseRules("Bash(rm:*)"),
	}
	if p.Decide(llm.ToolCall{Name: "bash", Arguments: json.RawMessage(`{"command":"ls"}`)}) != agent.PermitAllow {
		t.Fatal("ls should allow")
	}
	if p.Decide(llm.ToolCall{Name: "bash", Arguments: json.RawMessage(`{"command":"rm -rf /"}`)}) != agent.PermitDeny {
		t.Fatal("rm should deny")
	}
}

func TestToolPolicy_Allowlist(t *testing.T) {
	p := &agent.ToolPolicy{Allow: agent.ParseRules("read", "grep")}
	if p.Decide(llm.ToolCall{Name: "read"}) != agent.PermitAllow {
		t.Fatal("read allow")
	}
	if p.Decide(llm.ToolCall{Name: "bash"}) != agent.PermitDeny {
		t.Fatal("bash outside allowlist should deny")
	}
}

func TestToolPolicy_PlanAsks(t *testing.T) {
	p := &agent.ToolPolicy{Mode: agent.PolicyPlan}
	if p.Decide(llm.ToolCall{Name: "write"}) != agent.PermitAsk {
		t.Fatal("plan mode should ask")
	}
}

func TestToolPolicy_BypassIgnoresAsk(t *testing.T) {
	p := &agent.ToolPolicy{
		Mode: agent.PolicyBypass,
		Ask:  agent.ParseRules("bash"),
		Deny: agent.ParseRules("bash(rm:*)"),
	}
	if p.Decide(llm.ToolCall{Name: "bash", Arguments: json.RawMessage(`{"command":"ls"}`)}) != agent.PermitAllow {
		t.Fatal("bypass should allow ask-listed tools")
	}
	if p.Decide(llm.ToolCall{Name: "bash", Arguments: json.RawMessage(`{"command":"rm x"}`)}) != agent.PermitDeny {
		t.Fatal("bypass cannot skip deny")
	}
}

func TestToolPolicy_FilterHidesDenied(t *testing.T) {
	p := &agent.ToolPolicy{Deny: agent.ParseRules("bash")}
	tools := []agent.Tool{echoTool(), agent.Func("bash", "", nil, nil)}
	got := p.Filter(tools)
	if len(got) != 1 || got[0].Def.Name != "echo" {
		t.Fatalf("got %+v", got)
	}
}

func TestToolPolicy_AskPausesHITL(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo", Arguments: json.RawMessage(`{"x":1}`)}}},
		{Content: "done"},
	}}
	r := &agent.Runner{
		Client: fc,
		Tools:  []agent.Tool{echoTool()},
		Policy: &agent.ToolPolicy{Ask: agent.ParseRules("echo")},
	}
	out := agent.CollectOutcome(r.Run(context.Background(), llm.Request{Model: "m"}))
	if !out.IsPaused() || len(out.Requirements) != 1 {
		t.Fatalf("want paused, got %+v", out)
	}
	out = agent.CollectOutcome(r.Continue(context.Background(), out.RunID, []agent.Resolution{{ID: "c1", Approved: true}}))
	resp, err := out.Final()
	if err != nil || resp.Content != "done" {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
}

func TestToolPolicy_DenyFeedsModel(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo", Arguments: json.RawMessage(`{"x":1}`)}}},
		{Content: "ok"},
	}}
	r := &agent.Runner{
		Client: fc,
		Tools:  []agent.Tool{echoTool()},
		// 参数级 deny:仍广告 echo,执行时拒绝
		Policy: &agent.ToolPolicy{Deny: agent.ParseRules("echo(*1*)")},
	}
	out := agent.CollectOutcome(r.Run(context.Background(), llm.Request{Model: "m"}))
	resp, err := out.Final()
	if err != nil || resp.Content != "ok" {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
	var toolContent string
	for _, m := range fc.lastReq.Messages {
		if m.Role == llm.Tool {
			toolContent = m.Content
		}
	}
	if !strings.Contains(toolContent, "拒绝") {
		t.Fatalf("deny reason = %q", toolContent)
	}
}
