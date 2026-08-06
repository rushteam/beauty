package otelmq_test

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/rushteam/beauty/pkg/mq"
	"github.com/rushteam/beauty/pkg/mq/otelmq"
)

func setupTracer(t *testing.T) (*tracetest.InMemoryExporter, *sdktrace.TracerProvider) {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(sdktrace.NewTracerProvider())
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())
	})
	return exp, tp
}

type capturePub struct {
	last mq.Message
}

func (c *capturePub) Publish(_ context.Context, msg mq.Message) error {
	c.last = msg
	return nil
}

func TestPublisher_InjectsTraceparent(t *testing.T) {
	exp, tp := setupTracer(t)
	inner := &capturePub{}
	pub := otelmq.Publisher(inner, otelmq.WithTracerProvider(tp))

	ctx, parent := tp.Tracer("test").Start(t.Context(), "parent")
	defer parent.End()

	err := pub.Publish(ctx, mq.Message{Topic: "orders", Key: "u1", Body: []byte("x")})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if inner.last.Headers["traceparent"] == "" {
		t.Fatal("expected traceparent in message headers")
	}

	spans := exp.GetSpans()
	var found bool
	for _, s := range spans {
		if s.Name == "orders publish" {
			found = true
			if s.SpanKind != trace.SpanKindProducer {
				t.Errorf("span kind = %v, want Producer", s.SpanKind)
			}
			if s.Parent.TraceID() != parent.SpanContext().TraceID() {
				t.Error("publish span should be child of parent")
			}
		}
	}
	if !found {
		t.Fatal("missing \"orders publish\" span")
	}
}

func TestPublisher_DoesNotMutateCallerHeaders(t *testing.T) {
	_, tp := setupTracer(t)
	inner := &capturePub{}
	pub := otelmq.Publisher(inner, otelmq.WithTracerProvider(tp))

	orig := map[string]string{"x-a": "1"}
	ctx, span := tp.Tracer("test").Start(t.Context(), "parent")
	defer span.End()

	_ = pub.Publish(ctx, mq.Message{Topic: "t", Headers: orig, Body: []byte("x")})
	if _, ok := orig["traceparent"]; ok {
		t.Fatal("caller headers map must not be mutated")
	}
	if inner.last.Headers["x-a"] != "1" || inner.last.Headers["traceparent"] == "" {
		t.Fatalf("published headers incomplete: %#v", inner.last.Headers)
	}
}

func TestTrace_ContinuesTrace(t *testing.T) {
	exp, tp := setupTracer(t)
	prop := otel.GetTextMapPropagator()

	// 模拟发布端注入
	headers := map[string]string{}
	ctx, parent := tp.Tracer("test").Start(t.Context(), "parent")
	prop.Inject(ctx, propagation.MapCarrier(headers))
	parent.End()
	parentSC := parent.SpanContext()

	var gotTraceID trace.TraceID
	h := otelmq.Trace("order", otelmq.WithTracerProvider(tp))(func(ctx context.Context, _ mq.Message) error {
		gotTraceID = trace.SpanFromContext(ctx).SpanContext().TraceID()
		return nil
	})

	if err := h(context.Background(), mq.Message{Topic: "orders", Headers: headers}); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if gotTraceID != parentSC.TraceID() {
		t.Fatalf("trace id = %s, want %s (chain broken)", gotTraceID, parentSC.TraceID())
	}

	spans := exp.GetSpans()
	var found bool
	for _, s := range spans {
		if s.Name == "orders process" {
			found = true
			if s.SpanKind != trace.SpanKindConsumer {
				t.Errorf("span kind = %v, want Consumer", s.SpanKind)
			}
		}
	}
	if !found {
		t.Fatal("missing \"orders process\" span")
	}
}

func TestTrace_RecordsError(t *testing.T) {
	exp, tp := setupTracer(t)
	boom := errors.New("boom")
	h := otelmq.Trace("order", otelmq.WithTracerProvider(tp))(func(context.Context, mq.Message) error {
		return boom
	})
	if err := h(t.Context(), mq.Message{Topic: "t"}); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	for _, s := range exp.GetSpans() {
		if s.Name == "t process" {
			if s.Status.Code != codes.Error {
				t.Errorf("status = %v, want Error", s.Status.Code)
			}
			return
		}
	}
	t.Fatal("missing process span")
}

func TestEndToEnd_InProc(t *testing.T) {
	exp, tp := setupTracer(t)
	b := mq.NewInProc()
	pub := otelmq.Publisher(b, otelmq.WithTracerProvider(tp))

	done := make(chan trace.TraceID, 1)
	h := mq.Chain(func(ctx context.Context, _ mq.Message) error {
		done <- trace.SpanFromContext(ctx).SpanContext().TraceID()
		return nil
	}, otelmq.Trace("worker", otelmq.WithTracerProvider(tp)))

	subCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := b.Subscribe(subCtx, "jobs", h); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	ctx, parent := tp.Tracer("test").Start(t.Context(), "api")
	parentID := parent.SpanContext().TraceID()
	if err := pub.Publish(ctx, mq.Message{Topic: "jobs", Body: []byte("1")}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	parent.End()

	got := <-done
	if got != parentID {
		t.Fatalf("consumer trace = %s, want %s", got, parentID)
	}

	var pubSpan, procSpan bool
	for _, s := range exp.GetSpans() {
		switch s.Name {
		case "jobs publish":
			pubSpan = true
		case "jobs process":
			procSpan = true
		}
	}
	if !pubSpan || !procSpan {
		t.Fatalf("spans publish=%v process=%v", pubSpan, procSpan)
	}
}

func TestPublisher_NilInner(t *testing.T) {
	if otelmq.Publisher(nil) != nil {
		t.Fatal("nil inner should yield nil")
	}
}
