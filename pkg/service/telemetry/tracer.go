package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/rushteam/beauty/pkg/service/core"
)

var tracer trace.Tracer

type TraceOption func(c *traceComponent)

type traceComponent struct {
	provider trace.TracerProvider
	options  []sdktrace.TracerProviderOption
}

func (c *traceComponent) Name() string {
	return "tracer"
}

func WithTraceProvider(provider trace.TracerProvider) TraceOption {
	return func(o *traceComponent) {
		o.provider = provider
	}
}

func WithTraceProviderOption(opts ...sdktrace.TracerProviderOption) TraceOption {
	return func(o *traceComponent) {
		o.options = append(o.options, opts...)
	}
}

func WithTraceSampler(sampler sdktrace.Sampler) TraceOption {
	// sdktrace.ParentBased(sdktrace.TraceIDRatioBased(0.5))
	// sdktrace.ParentBased(sdktrace.AlwaysSample(), sdktrace.WithLocalParentNotSampled())
	return WithTraceProviderOption(sdktrace.WithSampler(sampler))
}

func WithTraceExporter(exporter sdktrace.SpanExporter, opts ...sdktrace.BatchSpanProcessorOption) TraceOption {
	return WithTraceProviderOption(sdktrace.WithBatcher(exporter, opts...))
}

func WithTraceStdoutExporter() TraceOption {
	exporter, err := stdouttrace.New(
		stdouttrace.WithPrettyPrint(),
	)
	if err != nil {
		panic(fmt.Sprintf("telemetry: failed to create stdout trace exporter: %v", err))
	}
	return WithTraceProviderOption(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
}

func (c *traceComponent) Init() context.CancelFunc {
	if c.provider == nil {
		c.provider = sdktrace.NewTracerProvider(c.options...)
	}

	otel.SetTracerProvider(c.provider)
	tracer = c.provider.Tracer("beauty")

	return func() {
		type shutdown interface {
			Shutdown(ctx context.Context) error
		}
		if p, ok := c.provider.(shutdown); ok {
			_ = p.Shutdown(context.Background())
		}
	}
}

func NewTracer(opts ...TraceOption) core.Component {
	c := &traceComponent{}
	// if len(opts) == 0 {
	// 	opts = append(opts, WithTraceStdoutExporter())
	// }
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func SpanFromContext(ctx context.Context, spanName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	if tracer == nil {
		tracer = otel.GetTracerProvider().Tracer("beauty")
	}
	return tracer.Start(ctx, spanName, opts...)
}
