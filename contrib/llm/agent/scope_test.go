package agent_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

func TestScopeByName(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{{Content: "done"}}}

	r := &agent.Runner{
		Client: fc,
		Tools: []agent.Tool{
			echoTool(),
			agent.Func("search", "搜索", nil, func(context.Context, json.RawMessage) (string, error) { return "found", nil }),
			agent.Func("delete", "删除", nil, func(context.Context, json.RawMessage) (string, error) { return "deleted", nil }),
		},
		Scope: agent.ScopeByName("echo", "search"),
	}

	r.Run(context.Background(), llm.Request{Model: "m"})

	if len(fc.lastReq.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d: %v", len(fc.lastReq.Tools), fc.lastReq.Tools)
	}
	names := map[string]bool{}
	for _, td := range fc.lastReq.Tools {
		names[td.Name] = true
	}
	if !names["echo"] || !names["search"] {
		t.Errorf("expected echo+search, got %v", names)
	}
	if names["delete"] {
		t.Error("delete should be excluded")
	}
}

func TestScopeExclude(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{{Content: "done"}}}

	r := &agent.Runner{
		Client: fc,
		Tools: []agent.Tool{
			echoTool(),
			agent.Func("dangerous", "危险", nil, func(context.Context, json.RawMessage) (string, error) { return "", nil }),
		},
		Scope: agent.ScopeExclude("dangerous"),
	}

	r.Run(context.Background(), llm.Request{Model: "m"})

	if len(fc.lastReq.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(fc.lastReq.Tools))
	}
	if fc.lastReq.Tools[0].Name != "echo" {
		t.Errorf("expected echo, got %s", fc.lastReq.Tools[0].Name)
	}
}

func TestScopeByStep(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo"}}},
		{Content: "done"},
	}}

	var step1Tools, step2Tools int
	r := &agent.Runner{
		Client: fc,
		Tools: []agent.Tool{
			echoTool(),
			agent.Func("write", "写入", nil, func(context.Context, json.RawMessage) (string, error) { return "ok", nil }),
		},
		Scope: agent.ScopeByStep(map[int][]string{
			1: {"echo"},
			2: {"echo", "write"},
		}),
		Hooks: agent.Hooks{
			BeforeModel: func(_ context.Context, step int, req *llm.Request) error {
				switch step {
				case 1:
					step1Tools = len(req.Tools)
				case 2:
					step2Tools = len(req.Tools)
				}
				return nil
			},
		},
	}

	r.Run(context.Background(), llm.Request{Model: "m"})

	if step1Tools != 1 {
		t.Errorf("step1 should have 1 tool, got %d", step1Tools)
	}
	if step2Tools != 2 {
		t.Errorf("step2 should have 2 tools, got %d", step2Tools)
	}
}

func TestChainScopes(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{{Content: "done"}}}

	r := &agent.Runner{
		Client: fc,
		Tools: []agent.Tool{
			echoTool(),
			agent.Func("a", "", nil, func(context.Context, json.RawMessage) (string, error) { return "", nil }),
			agent.Func("b", "", nil, func(context.Context, json.RawMessage) (string, error) { return "", nil }),
		},
		Scope: agent.ChainScopes(
			agent.ScopeExclude("b"),
			agent.ScopeByName("echo"),
		),
	}

	r.Run(context.Background(), llm.Request{Model: "m"})

	if len(fc.lastReq.Tools) != 1 || fc.lastReq.Tools[0].Name != "echo" {
		t.Fatalf("expected only echo, got %v", fc.lastReq.Tools)
	}
}
