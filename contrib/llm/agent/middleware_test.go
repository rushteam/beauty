package agent_test

import (
	"context"
	"iter"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

func TestChainMiddleware(t *testing.T) {
	var order []string

	mw1 := agent.AgentMiddleware(func(next agent.AgentRunFunc) agent.AgentRunFunc {
		return agent.AgentRunFunc(func(ctx context.Context, req llm.Request, opts ...agent.Option) iter.Seq2[agent.Event, error] {
			order = append(order, "mw1-before")
			return iter.Seq2[agent.Event, error](func(yield func(agent.Event, error) bool) {
				for ev, err := range next(ctx, req, opts...) {
					if !yield(ev, err) {
						return
					}
				}
				order = append(order, "mw1-after")
			})
		})
	})

	mw2 := agent.AgentMiddleware(func(next agent.AgentRunFunc) agent.AgentRunFunc {
		return agent.AgentRunFunc(func(ctx context.Context, req llm.Request, opts ...agent.Option) iter.Seq2[agent.Event, error] {
			order = append(order, "mw2-before")
			return iter.Seq2[agent.Event, error](func(yield func(agent.Event, error) bool) {
				for ev, err := range next(ctx, req, opts...) {
					if !yield(ev, err) {
						return
					}
				}
				order = append(order, "mw2-after")
			})
		})
	})

	core := agent.AgentRunFunc(func(_ context.Context, _ llm.Request, _ ...agent.Option) iter.Seq2[agent.Event, error] {
		return iter.Seq2[agent.Event, error](func(yield func(agent.Event, error) bool) {
			order = append(order, "core")
			yield(agent.Event{Type: agent.EventFinal}, nil)
		})
	})

	chained := agent.ChainMiddleware(mw1, mw2)(core)
	for range chained(context.Background(), llm.Request{}) {
	}

	expected := []string{"mw1-before", "mw2-before", "core", "mw2-after", "mw1-after"}
	if len(order) != len(expected) {
		t.Fatalf("order = %v, want %v", order, expected)
	}
	for i, e := range expected {
		if order[i] != e {
			t.Errorf("order[%d] = %q, want %q", i, order[i], e)
		}
	}
}

func TestLoggingMiddleware(t *testing.T) {
	var events []string
	logFn := func(_ context.Context, event string, _ map[string]any) {
		events = append(events, event)
	}

	core := agent.AgentRunFunc(func(_ context.Context, _ llm.Request, _ ...agent.Option) iter.Seq2[agent.Event, error] {
		return iter.Seq2[agent.Event, error](func(yield func(agent.Event, error) bool) {
			if !yield(agent.Event{Type: agent.EventStep, Response: &llm.Response{Content: "x"}}, nil) {
				return
			}
			yield(agent.Event{Type: agent.EventFinal, Response: &llm.Response{Content: "done", Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}}}, nil)
		})
	})

	fn := agent.LoggingMiddleware(logFn)(core)
	for range fn(context.Background(), llm.Request{Model: "test"}) {
	}

	if len(events) != 2 {
		t.Fatalf("events = %v, want 2 entries", events)
	}
	if events[0] != "agent.run.start" {
		t.Errorf("events[0] = %q, want 'agent.run.start'", events[0])
	}
	if events[1] != "agent.run.done" {
		t.Errorf("events[1] = %q, want 'agent.run.done'", events[1])
	}
}

func TestSourceAttributionMiddleware(t *testing.T) {
	core := agent.AgentRunFunc(func(_ context.Context, _ llm.Request, _ ...agent.Option) iter.Seq2[agent.Event, error] {
		return iter.Seq2[agent.Event, error](func(yield func(agent.Event, error) bool) {
			if !yield(agent.Event{Type: agent.EventStep}, nil) {
				return
			}
			yield(agent.Event{Type: agent.EventFinal, AgentName: "explicit"}, nil)
		})
	})

	fn := agent.SourceAttributionMiddleware("test-agent")(core)
	var names []string
	for ev := range fn(context.Background(), llm.Request{}) {
		names = append(names, ev.AgentName)
	}

	if len(names) != 2 {
		t.Fatalf("names = %v, want 2", names)
	}
	if names[0] != "test-agent" {
		t.Errorf("names[0] = %q, want 'test-agent' (auto-filled)", names[0])
	}
	if names[1] != "explicit" {
		t.Errorf("names[1] = %q, want 'explicit' (preserved)", names[1])
	}
}
