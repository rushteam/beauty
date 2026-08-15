package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

func TestHooks_BeforeAfterTurn(t *testing.T) {
	var log []string
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo"}}},
		{Content: "done"},
	}}
	r := &agent.Runner{
		Client: fc,
		Tools:  []agent.Tool{echoTool()},
		Hooks: agent.Hooks{
			BeforeTurn: func(ctx context.Context, req *llm.Request) error {
				log = append(log, "before_turn")
				req.System = "injected-system"
				return nil
			},
			AfterTurn: func(ctx context.Context, out *agent.RunOutcome) {
				log = append(log, "after_turn:"+string(out.Status))
			},
			BeforeModel: func(context.Context, int, *llm.Request) error {
				log = append(log, "bm")
				return nil
			},
		},
	}

	out := agent.CollectOutcome(r.Run(context.Background(), llm.Request{Model: "m"}))
	if _, err := out.Final(); err != nil {
		t.Fatal(err)
	}

	if len(log) < 3 {
		t.Fatalf("log=%v", log)
	}
	if log[0] != "before_turn" {
		t.Errorf("first should be before_turn, got %q", log[0])
	}
	if log[len(log)-1] != "after_turn:done" {
		t.Errorf("last should be after_turn:done, got %q", log[len(log)-1])
	}
	if fc.lastReq.System != "injected-system" {
		t.Errorf("expected modified system, got %q", fc.lastReq.System)
	}
}

func TestHooks_BeforeTurnError(t *testing.T) {
	want := errors.New("turn rejected")
	var afterCalled bool
	r := &agent.Runner{
		Client: &fakeClient{steps: []*llm.Response{{Content: "x"}}},
		Hooks: agent.Hooks{
			BeforeTurn: func(context.Context, *llm.Request) error { return want },
			AfterTurn:  func(_ context.Context, _ *agent.RunOutcome) { afterCalled = true },
		},
	}

	out := agent.CollectOutcome(r.Run(context.Background(), llm.Request{Model: "m"}))
	if !errors.Is(out.Err, want) {
		t.Fatalf("expected %v, got %v", want, out.Err)
	}
	if !afterCalled {
		t.Error("AfterTurn should be called even on error")
	}
}

// streamFakeClient 支持 Stream,用于测试 OnChunk。
type streamFakeClient struct {
	chunks []llm.Chunk
}

func (f *streamFakeClient) Generate(context.Context, llm.Request) (*llm.Response, error) {
	return nil, errors.New("unused")
}

func (f *streamFakeClient) Stream(_ context.Context, _ llm.Request) iter.Seq2[llm.Chunk, error] {
	chunks := f.chunks
	return func(yield func(llm.Chunk, error) bool) {
		for _, c := range chunks {
			if !yield(c, nil) {
				return
			}
		}
	}
}

func TestHooks_OnChunk(t *testing.T) {
	sfc := &streamFakeClient{
		chunks: []llm.Chunk{
			{Delta: "hello "},
			{Delta: "world"},
			{Usage: &llm.Usage{InputTokens: 10, OutputTokens: 5}},
		},
	}

	var captured []string
	r := &agent.Runner{
		Client: sfc,
		Hooks: agent.Hooks{
			OnChunk: func(ctx context.Context, step int, chunk *llm.Chunk) error {
				if chunk.Delta != "" {
					captured = append(captured, chunk.Delta)
					chunk.Delta = "[filtered]"
				}
				return nil
			},
		},
	}

	var tokens []string
	for ev, _ := range r.Run(context.Background(), llm.Request{Model: "m"}) {
		if ev.Type == agent.EventToken {
			tokens = append(tokens, ev.Result)
		}
	}

	if len(captured) != 2 || captured[0] != "hello " || captured[1] != "world" {
		t.Errorf("captured = %v", captured)
	}
	for _, tok := range tokens {
		if tok != "[filtered]" {
			t.Errorf("expected [filtered], got %q", tok)
		}
	}
}

func TestHooks_OnChunkError(t *testing.T) {
	sfc := &streamFakeClient{
		chunks: []llm.Chunk{
			{Delta: "hello"},
			{Usage: &llm.Usage{InputTokens: 1, OutputTokens: 2}},
		},
	}

	r := &agent.Runner{
		Client: sfc,
		Hooks: agent.Hooks{
			OnChunk: func(context.Context, int, *llm.Chunk) error {
				return errors.New("content policy violation")
			},
		},
	}

	var errSeen bool
	for ev, _ := range r.Run(context.Background(), llm.Request{Model: "m"}) {
		if ev.Type == agent.EventError && ev.Err != nil {
			errSeen = true
		}
	}
	if !errSeen {
		t.Error("expected error event from OnChunk")
	}
}

func TestHooks_BeforeTool_Deny(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "dangerous_tool", Arguments: json.RawMessage(`{}`)}}},
		{Content: "ok understood"},
	}}

	r := &agent.Runner{
		Client: fc,
		Tools: []agent.Tool{
			agent.Func("dangerous_tool", "危险操作", nil, func(context.Context, json.RawMessage) (string, error) {
				t.Fatal("should not be called")
				return "", nil
			}),
		},
		Hooks: agent.Hooks{
			BeforeTool: func(_ context.Context, _ int, tc *llm.ToolCall) (agent.Permission, error) {
				if tc.Name == "dangerous_tool" {
					return agent.PermitDeny, nil
				}
				return agent.PermitAllow, nil
			},
		},
	}

	out := agent.CollectOutcome(r.Run(context.Background(), llm.Request{Model: "m"}))
	resp, err := out.Final()
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "ok understood" {
		t.Errorf("unexpected content: %s", resp.Content)
	}
}

func TestHooks_BeforeTool_ModifyArgs(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo", Arguments: json.RawMessage(`{"secret":"password123"}`)}}},
		{Content: "done"},
	}}

	var receivedArgs string
	tool := agent.Func("echo", "回显", nil, func(_ context.Context, args json.RawMessage) (string, error) {
		receivedArgs = string(args)
		return "ok", nil
	})

	r := &agent.Runner{
		Client: fc,
		Tools:  []agent.Tool{tool},
		Hooks: agent.Hooks{
			BeforeTool: func(_ context.Context, _ int, tc *llm.ToolCall) (agent.Permission, error) {
				tc.Arguments = json.RawMessage(`{"secret":"[REDACTED]"}`)
				return agent.PermitAllow, nil
			},
		},
	}

	agent.CollectOutcome(r.Run(context.Background(), llm.Request{Model: "m"}))

	if receivedArgs != `{"secret":"[REDACTED]"}` {
		t.Errorf("BeforeTool should have rewritten args, got %s", receivedArgs)
	}
}

func TestHooks_AfterTool_ModifyResult(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo", Arguments: json.RawMessage(`{}`)}}},
		{Content: "done"},
	}}

	r := &agent.Runner{
		Client: fc,
		Tools:  []agent.Tool{echoTool()},
		Hooks: agent.Hooks{
			AfterTool: func(_ context.Context, _ int, _ llm.ToolCall, result *string) error {
				*result = "[FILTERED]"
				return nil
			},
		},
	}

	agent.CollectOutcome(r.Run(context.Background(), llm.Request{Model: "m"}))

	// 模型第二轮收到的 tool result 应被改写
	msgs := fc.lastReq.Messages
	toolMsg := msgs[len(msgs)-1]
	if toolMsg.Content != "[FILTERED]" {
		t.Errorf("expected [FILTERED], got %q", toolMsg.Content)
	}
}
