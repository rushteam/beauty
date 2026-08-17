package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

func TestRunner_RunStream(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo", Arguments: json.RawMessage(`{"x":1}`)}}},
		{Content: "done"},
	}}
	r := &agent.Runner{Client: fc, Tools: []agent.Tool{echoTool()}}

	var types []agent.EventType
	var final string
	for ev := range r.Run(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.User, Content: "go"}}}) {
		types = append(types, ev.Type)
		if ev.Type == agent.EventFinal && ev.Response != nil {
			final = ev.Response.Content
		}
		if ev.Type == agent.EventError {
			t.Fatalf("unexpected error event: %v", ev.Err)
		}
	}
	if final != "done" {
		t.Fatalf("final=%q", final)
	}
	want := []agent.EventType{agent.EventStep, agent.EventToolStart, agent.EventToolResult, agent.EventStep, agent.EventFinal}
	if len(types) != len(want) {
		t.Fatalf("events=%v want %v", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("event[%d]=%s want %s (all=%v)", i, types[i], want[i], types)
		}
	}
}

func TestRunner_Cancel(t *testing.T) {
	block := make(chan struct{})
	entered := make(chan struct{})
	fc := &blockingClient{block: block, entered: entered}
	r := &agent.Runner{Client: fc}

	ctx, cancel := context.WithCancel(context.Background())
	ch := r.Run(ctx, llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.User, Content: "x"}}})

	var gotErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range ch {
			if ev.Type == agent.EventError {
				gotErr = ev.Err
			}
		}
	}()

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("Stream 未进入")
	}
	cancel()
	close(block)
	<-done
	if !errors.Is(gotErr, context.Canceled) {
		t.Fatalf("want canceled, got %v", gotErr)
	}
}

type blockingClient struct {
	entered chan struct{}
	block   chan struct{}
	once    bool
}

func (b *blockingClient) Generate(ctx context.Context, _ llm.Request) (*llm.Response, error) {
	if !b.once {
		b.once = true
		close(b.entered)
		select {
		case <-b.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &llm.Response{Content: "ok"}, nil
}

func (b *blockingClient) Stream(ctx context.Context, _ llm.Request) iter.Seq2[llm.Chunk, error] {
	return func(yield func(llm.Chunk, error) bool) {
		if !b.once {
			b.once = true
			close(b.entered)
			select {
			case <-b.block:
			case <-ctx.Done():
				yield(llm.Chunk{}, ctx.Err())
				return
			}
		}
		if err := ctx.Err(); err != nil {
			yield(llm.Chunk{}, err)
			return
		}
		yield(llm.Chunk{Delta: "ok"}, nil)
	}
}

func TestPermission_Deny(t *testing.T) {
	denied := echoTool()
	denied.Permission = agent.PermitDeny
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo"}}},
		{Content: "ok"},
	}}
	r := &agent.Runner{Client: fc, Tools: []agent.Tool{denied}}
	if _, err := agent.CollectOutcome(r.Run(context.Background(), llm.Request{Model: "m"})).Final(); err != nil {
		t.Fatal(err)
	}
	got := fc.lastReq.Messages[len(fc.lastReq.Messages)-1].Content
	if !strings.Contains(got, "deny") && !strings.Contains(got, "拒绝") {
		t.Fatalf("deny result=%q", got)
	}
}

func TestPermission_AskViaPermitAsk(t *testing.T) {
	ttool := echoTool()
	ttool.Permission = agent.PermitAsk
	called := false
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo", Arguments: json.RawMessage(`{}`)}}},
		{Content: "done"},
	}}
	inner := &agent.Runner{Client: fc, Tools: []agent.Tool{ttool}}
	r := agent.SyncHITL(inner, func(context.Context, llm.ToolCall) (agent.Resolution, error) {
		called = true
		return agent.Resolution{Approved: true}, nil
	})
	if _, err := agent.CollectOutcome(r.Run(context.Background(), llm.Request{Model: "m"})).Final(); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("PermitAsk 应触发 Ask")
	}
}

func TestAgentAsTool(t *testing.T) {
	subFC := &fakeClient{steps: []*llm.Response{{Content: "sub-answer"}}}
	sub := &agent.Runner{Client: subFC}
	parentFC := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "researcher", Arguments: json.RawMessage(`{"input":"q"}`)}}},
		{Content: "parent-done"},
	}}
	parent := &agent.Runner{
		Client: parentFC,
		Tools:  []agent.Tool{agent.AgentAsTool("researcher", "研究", sub, agent.WithAgentToolModel("m"))},
	}
	out := agent.CollectOutcome(parent.Run(context.Background(), llm.Request{Model: "m"}))
	resp, err := out.Final()
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "parent-done" {
		t.Fatalf("content=%q", resp.Content)
	}
	toolMsg := parentFC.lastReq.Messages[len(parentFC.lastReq.Messages)-1]
	if toolMsg.Content != "sub-answer" {
		t.Fatalf("tool result=%q", toolMsg.Content)
	}
}

func TestChain(t *testing.T) {
	a := &agent.Runner{Client: &fakeClient{steps: []*llm.Response{{Content: "draft"}}}}
	b := &agent.Runner{Client: &fakeClient{steps: []*llm.Response{{Content: "polished"}}}}
	ch := &agent.Chain{Steps: []agent.ChainStep{
		{Name: "draft", Runner: a, Model: "m"},
		{Name: "polish", Runner: b, Model: "m", System: "polish"},
	}}
	out := agent.CollectOutcome(ch.Run(context.Background(), llm.Request{
		Model:    "m",
		Messages: []llm.Message{{Role: llm.User, Content: "write"}},
	}))
	resp, err := out.Final()
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "polished" {
		t.Fatalf("content=%q", resp.Content)
	}
}
