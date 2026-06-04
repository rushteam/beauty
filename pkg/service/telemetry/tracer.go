package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/rushteam/beauty/pkg/service/core"
)

var tracer trace.Tracer

type TraceOption func(c *traceComponent)

type traceComponent struct {
	provider    trace.TracerProvider
	options     []sdktrace.TracerProviderOption
	propagators []propagation.TextMapPropagator
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

// WithTracePropagator 追加 trace context 传播协议。
// 默认已启用 W3C TraceContext + Baggage（OTel 推荐组合），调用此方法可追加或替换。
//
// 常用示例：
//
//	// 追加 B3（兼容 Zipkin/Jaeger 老版本）
//	import "go.opentelemetry.io/contrib/propagators/b3"
//	WithTracePropagator(b3.New())
//
//	// 只用 B3，不用 W3C（需先调用 WithTracePropagator 再在 Init 里覆盖默认值）
//	WithTracePropagator(b3.New(b3.WithInjectEncoding(b3.B3SingleHeader)))
func WithTracePropagator(p ...propagation.TextMapPropagator) TraceOption {
	return func(o *traceComponent) {
		o.propagators = append(o.propagators, p...)
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

// WithTraceOTLPGRPCExporter 通过 OTLP/gRPC（默认端口 4317）上报 trace 到 Collector
// （otel-collector / Tempo / Jaeger 等）。不传 opts 时自动读取标准 OTEL_EXPORTER_OTLP_* 环境变量；
// 也可显式配置：
//
//	WithTraceOTLPGRPCExporter(
//		otlptracegrpc.WithEndpoint("otel-collector:4317"),
//		otlptracegrpc.WithInsecure(), // 无 TLS 的内网 Collector
//	)
func WithTraceOTLPGRPCExporter(opts ...otlptracegrpc.Option) TraceOption {
	exporter, err := otlptracegrpc.New(context.Background(), opts...)
	if err != nil {
		panic(fmt.Sprintf("telemetry: failed to create OTLP/gRPC trace exporter: %v", err))
	}
	return WithTraceProviderOption(sdktrace.WithBatcher(exporter))
}

// WithTraceOTLPHTTPExporter 通过 OTLP/HTTP（默认端口 4318）上报 trace。
// 不传 opts 时自动读取标准 OTEL_EXPORTER_OTLP_* 环境变量；也可显式配置：
//
//	WithTraceOTLPHTTPExporter(
//		otlptracehttp.WithEndpoint("otel-collector:4318"),
//		otlptracehttp.WithInsecure(),
//	)
func WithTraceOTLPHTTPExporter(opts ...otlptracehttp.Option) TraceOption {
	exporter, err := otlptracehttp.New(context.Background(), opts...)
	if err != nil {
		panic(fmt.Sprintf("telemetry: failed to create OTLP/HTTP trace exporter: %v", err))
	}
	return WithTraceProviderOption(sdktrace.WithBatcher(exporter))
}

func (c *traceComponent) Init() context.CancelFunc {
	setupErrorHandler() // 把异步导出错误接到项目 logger，否则会被默默打到 stderr
	if c.provider == nil {
		c.provider = sdktrace.NewTracerProvider(c.options...)
	}
	otel.SetTracerProvider(c.provider)
	tracer = c.provider.Tracer("beauty")

	// 设置 TextMapPropagator：默认 W3C TraceContext + Baggage，可通过 WithTracePropagator 追加。
	// 没有这一步，otelhttp/otelgrpc 无法在服务间传递 trace，链路会断开。
	props := []propagation.TextMapPropagator{
		propagation.TraceContext{}, // W3C traceparent / tracestate
		propagation.Baggage{},     // W3C baggage
	}
	props = append(props, c.propagators...)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(props...))

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
