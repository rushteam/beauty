package otelllm

import (
	"context"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestNewAgentHooks(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))

	hooks := NewAgentHooks(WithHookTracerProvider(tp))

	ctx := context.Background()

	req := &llm.Request{Model: "gpt-4", Messages: []llm.Message{{Role: llm.User, Content: "hi"}}}
	if err := hooks.BeforeModel(ctx, 1, req); err != nil {
		t.Fatal(err)
	}
	resp := &llm.Response{
		Model: "gpt-4",
		Usage: llm.Usage{InputTokens: 100, OutputTokens: 50},
	}
	if err := hooks.AfterModel(ctx, 1, resp); err != nil {
		t.Fatal(err)
	}

	tc := &llm.ToolCall{ID: "call_1", Name: "query_db"}
	perm, err := hooks.BeforeTool(ctx, 1, tc)
	if err != nil {
		t.Fatal(err)
	}
	if perm != agent.PermitAllow {
		t.Fatalf("expected PermitAllow, got %v", perm)
	}
	result := `{"rows": 42}`
	if err := hooks.AfterTool(ctx, 1, *tc, &result); err != nil {
		t.Fatal(err)
	}

	tp.ForceFlush(ctx)
	spans := exp.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(spans))
	}
	if spans[0].Name != "agent.model gpt-4 step=1" {
		t.Errorf("span[0].Name = %q", spans[0].Name)
	}
	if spans[1].Name != "agent.tool query_db" {
		t.Errorf("span[1].Name = %q", spans[1].Name)
	}
}

func TestMergeHooks(t *testing.T) {
	var order []string

	makeHooks := func(prefix string) agent.Hooks {
		return agent.Hooks{
			BeforeModel: func(context.Context, int, *llm.Request) error {
				order = append(order, prefix+":bm")
				return nil
			},
			AfterModel: func(context.Context, int, *llm.Response) error {
				order = append(order, prefix+":am")
				return nil
			},
			BeforeTool: func(_ context.Context, _ int, _ *llm.ToolCall) (agent.Permission, error) {
				order = append(order, prefix+":bt")
				return agent.PermitAllow, nil
			},
			AfterTool: func(_ context.Context, _ int, _ llm.ToolCall, _ *string) error {
				order = append(order, prefix+":at")
				return nil
			},
		}
	}

	merged := MergeHooks(makeHooks("a"), makeHooks("b"))

	ctx := context.Background()
	merged.BeforeModel(ctx, 1, &llm.Request{})
	merged.AfterModel(ctx, 1, &llm.Response{})
	tc := &llm.ToolCall{}
	merged.BeforeTool(ctx, 1, tc)
	result := "ok"
	merged.AfterTool(ctx, 1, *tc, &result)

	want := "a:bm b:bm a:am b:am a:bt b:bt a:at b:at"
	got := ""
	for i, s := range order {
		if i > 0 {
			got += " "
		}
		got += s
	}
	if got != want {
		t.Errorf("order = %q, want %q", got, want)
	}
}
