package otelllm

import (
	"context"
	"errors"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// --- fake client ---

type fakeClient struct {
	resp *llm.Response
	err  error
}

func (f *fakeClient) Generate(_ context.Context, _ llm.Request) (*llm.Response, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

func (f *fakeClient) Stream(_ context.Context, req llm.Request) (<-chan llm.Chunk, error) {
	if f.err != nil {
		return nil, f.err
	}
	ch := make(chan llm.Chunk, 2)
	ch <- llm.Chunk{Delta: "hello"}
	ch <- llm.Chunk{
		Done:  true,
		Usage: &f.resp.Usage,
	}
	close(ch)
	return ch, nil
}

// --- tests ---

func TestInstrument_Generate_Success(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer tp.Shutdown(context.Background())

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer mp.Shutdown(context.Background())

	fake := &fakeClient{
		resp: &llm.Response{
			Content:    "Hello, world!",
			Model:      "gpt-4",
			StopReason: "stop",
			Usage:      llm.Usage{InputTokens: 100, OutputTokens: 50},
		},
	}

	client := Instrument(fake,
		WithSystem("openai"),
		WithTracerProvider(tp),
		WithMeterProvider(mp),
		WithRecordPrompt(),
	)

	resp, err := client.Generate(context.Background(), llm.Request{
		Model: "gpt-4",
		Messages: []llm.Message{
			{Role: llm.User, Content: "Say hello"},
		},
		System:      "You are a helpful assistant",
		MaxTokens:   1000,
		Temperature: 0.7,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "Hello, world!" {
		t.Fatalf("unexpected content: %s", resp.Content)
	}

	// 验证 span
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	s := spans[0]
	if s.Name != "chat gpt-4" {
		t.Errorf("expected span name 'chat gpt-4', got %q", s.Name)
	}

	attrMap := spanAttrMap(s.Attributes)
	assertAttr(t, attrMap, "gen_ai.system", "openai")
	assertAttr(t, attrMap, "gen_ai.request.model", "gpt-4")
	assertAttr(t, attrMap, "gen_ai.response.model", "gpt-4")
	assertIntAttr(t, attrMap, "gen_ai.usage.input_tokens", 100)
	assertIntAttr(t, attrMap, "gen_ai.usage.output_tokens", 50)
	assertIntAttr(t, attrMap, "gen_ai.request.max_tokens", 1000)

	// 验证 prompt events
	if len(s.Events) < 2 {
		t.Fatalf("expected at least 2 events (system + user), got %d", len(s.Events))
	}
	if s.Events[0].Name != "gen_ai.system.message" {
		t.Errorf("expected first event name 'gen_ai.system.message', got %q", s.Events[0].Name)
	}
	if s.Events[1].Name != "gen_ai.user.message" {
		t.Errorf("expected second event name 'gen_ai.user.message', got %q", s.Events[1].Name)
	}

	// 验证 metrics
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("failed to collect metrics: %v", err)
	}
	foundDuration := false
	foundTokenInput := false
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch m.Name {
			case "gen_ai.client.operation.duration.count":
				foundDuration = true
			case "gen_ai.client.token.usage.input":
				foundTokenInput = true
			}
		}
	}
	if !foundDuration {
		t.Error("expected gen_ai.client.operation.duration.count metric")
	}
	if !foundTokenInput {
		t.Error("expected gen_ai.client.token.usage.input metric")
	}
}

func TestInstrument_Generate_Error(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer tp.Shutdown(context.Background())

	mp := sdkmetric.NewMeterProvider()
	defer mp.Shutdown(context.Background())

	fake := &fakeClient{err: errors.New("rate limited")}
	client := Instrument(fake,
		WithSystem("anthropic"),
		WithTracerProvider(tp),
		WithMeterProvider(mp),
	)

	_, err := client.Generate(context.Background(), llm.Request{Model: "claude-3"})
	if err == nil {
		t.Fatal("expected error")
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if spans[0].Status.Code != codes.Error {
		t.Errorf("expected error status, got %v", spans[0].Status.Code)
	}
}

func TestInstrument_Stream_Success(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer tp.Shutdown(context.Background())

	mp := sdkmetric.NewMeterProvider()
	defer mp.Shutdown(context.Background())

	fake := &fakeClient{
		resp: &llm.Response{
			Model: "gpt-4o",
			Usage: llm.Usage{InputTokens: 200, OutputTokens: 80},
		},
	}
	client := Instrument(fake,
		WithSystem("openai"),
		WithTracerProvider(tp),
		WithMeterProvider(mp),
	)

	ch, err := client.Stream(context.Background(), llm.Request{Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var chunks int
	for range ch {
		chunks++
	}
	if chunks != 2 {
		t.Errorf("expected 2 chunks, got %d", chunks)
	}

	// span 在 goroutine 中结束,等 exporter 刷新
	tp.ForceFlush(context.Background())

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	attrMap := spanAttrMap(spans[0].Attributes)
	assertIntAttr(t, attrMap, "gen_ai.usage.input_tokens", 200)
	assertIntAttr(t, attrMap, "gen_ai.usage.output_tokens", 80)
}

func TestInstrument_Stream_Error(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer tp.Shutdown(context.Background())

	mp := sdkmetric.NewMeterProvider()
	defer mp.Shutdown(context.Background())

	fake := &fakeClient{err: errors.New("connection refused")}
	client := Instrument(fake,
		WithTracerProvider(tp),
		WithMeterProvider(mp),
	)

	_, err := client.Stream(context.Background(), llm.Request{Model: "gpt-4"})
	if err == nil {
		t.Fatal("expected error")
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if spans[0].Status.Code != codes.Error {
		t.Errorf("expected error status, got %v", spans[0].Status.Code)
	}
}

// helpers

func spanAttrMap(attrs []attribute.KeyValue) map[string]attribute.Value {
	m := make(map[string]attribute.Value, len(attrs))
	for _, a := range attrs {
		m[string(a.Key)] = a.Value
	}
	return m
}

func assertAttr(t *testing.T, m map[string]attribute.Value, key, want string) {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Errorf("missing attribute %q", key)
		return
	}
	if v.AsString() != want {
		t.Errorf("attribute %q = %q, want %q", key, v.AsString(), want)
	}
}

func assertIntAttr(t *testing.T, m map[string]attribute.Value, key string, want int) {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Errorf("missing attribute %q", key)
		return
	}
	if v.AsInt64() != int64(want) {
		t.Errorf("attribute %q = %d, want %d", key, v.AsInt64(), want)
	}
}
