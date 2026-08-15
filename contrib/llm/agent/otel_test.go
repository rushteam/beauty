package agent_test

import (
	"context"
	"iter"
	"sync"
	"testing"
	"time"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

type recordingTracer struct {
	mu    sync.Mutex
	spans []string
}

func (r *recordingTracer) Start(ctx context.Context, name string, _ agent.SpanKind) (context.Context, agent.Span) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.spans = append(r.spans, name)
	noop := agent.NoOpTracer{}
	_, span := noop.Start(ctx, name, 0)
	return ctx, span
}

type recordingMetrics struct {
	mu        sync.Mutex
	durations []string
	tokens    []llm.Usage
}

func (r *recordingMetrics) RecordDuration(_ context.Context, name string, _ time.Duration, _ map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.durations = append(r.durations, name)
}

func (r *recordingMetrics) RecordTokens(_ context.Context, _ string, u llm.Usage, _ map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tokens = append(r.tokens, u)
}

func (r *recordingMetrics) RecordToolCall(_ context.Context, _ string, _ time.Duration, _ error) {
}

func TestOTelMiddleware(t *testing.T) {
	tracer := &recordingTracer{}
	metrics := &recordingMetrics{}

	core := agent.AgentRunFunc(func(_ context.Context, _ llm.Request, _ ...agent.Option) iter.Seq2[agent.Event, error] {
		return iter.Seq2[agent.Event, error](func(yield func(agent.Event, error) bool) {
			yield(agent.Event{
				Type:     agent.EventStep,
				Step:     1,
				Response: &llm.Response{Usage: llm.Usage{InputTokens: 100, OutputTokens: 50}},
			}, nil)
			yield(agent.Event{
				Type:     agent.EventFinal,
				Response: &llm.Response{Content: "done"},
			}, nil)
		})
	})

	fn := agent.OTelMiddleware(agent.OTelConfig{
		Tracer:  tracer,
		Metrics: metrics,
	})(core)

	for range fn(context.Background(), llm.Request{Model: "test"}) {
	}

	tracer.mu.Lock()
	defer tracer.mu.Unlock()
	if len(tracer.spans) != 1 || tracer.spans[0] != "agent.run" {
		t.Errorf("spans = %v, want [agent.run]", tracer.spans)
	}

	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	if len(metrics.durations) != 1 || metrics.durations[0] != "agent.run.duration" {
		t.Errorf("durations = %v, want [agent.run.duration]", metrics.durations)
	}
}

func TestOTelClientMiddleware(t *testing.T) {
	metrics := &recordingMetrics{}
	stub := &stubLLMClient{response: &llm.Response{
		Content: "hi",
		Model:   "test",
		Usage:   llm.Usage{InputTokens: 10, OutputTokens: 5},
	}}

	wrapped := agent.OTelClientMiddleware(stub, agent.OTelConfig{Metrics: metrics})
	resp, err := wrapped.Generate(context.Background(), llm.Request{Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hi" {
		t.Errorf("content = %q", resp.Content)
	}

	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	if len(metrics.durations) != 1 {
		t.Errorf("durations = %d, want 1", len(metrics.durations))
	}
	if len(metrics.tokens) != 1 {
		t.Errorf("tokens = %d, want 1", len(metrics.tokens))
	}
}

func TestNoOpTracer(t *testing.T) {
	tracer := agent.NoOpTracer{}
	ctx, span := tracer.Start(context.Background(), "test", agent.SpanAgent)
	if ctx == nil {
		t.Error("context should not be nil")
	}
	span.SetAttribute("key", "value")
	span.RecordError(nil)
	span.End()
}

type stubLLMClient struct {
	response *llm.Response
}

func (s *stubLLMClient) Generate(_ context.Context, _ llm.Request) (*llm.Response, error) {
	return s.response, nil
}

func (s *stubLLMClient) Stream(_ context.Context, _ llm.Request) iter.Seq2[llm.Chunk, error] {
	ch := make(chan llm.Chunk, 1)
	ch <- llm.Chunk{Done: true, Delta: s.response.Content, Usage: &s.response.Usage}
	close(ch)
	return ch, nil
}
