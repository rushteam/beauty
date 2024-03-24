package tracing

import (
	"context"
	"log"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"

	// "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	// semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"github.com/rushteam/beauty/pkg/core"
	"go.opentelemetry.io/otel/trace"
)

var tracer trace.Tracer

type traceComponent struct{}

func (c *traceComponent) Name() string {
	return "tracer"
}

func (c *traceComponent) Init() context.CancelFunc {
	return newTracer()
}

func NewTracer() core.Component {
	return &traceComponent{}
}

func newTracer() context.CancelFunc {
	// exporter, err := jaeger.NewRawExporter(
	// 	jaeger.WithCollectorEndpoint("http://your-jaeger-collector-endpoint:14268/api/traces"),
	// 	jaeger.WithProcess(jaeger.Process{
	// 		ServiceName: "your-service-name",
	// 	}),
	// )
	// if err != nil {
	// 	log.Fatalf("failed to initialize Jaeger exporter: %v", err)
	// }
	exporter, err := stdouttrace.New(
		stdouttrace.WithPrettyPrint(),
	)
	if err != nil {
		log.Fatal(err)
	}
	// res, err := resource.Merge(
	// 	resource.Environment(),
	// 	resource.NewSchemaless(
	// 		semconv.ServiceName("beauty"),
	// 		semconv.ServiceVersion("0.0.1"),
	// 		// semconv.SchemaURL,
	// 		// semconv.ServerAddress(),
	// 		// semconv.ServerPort(),
	// 		// semconv.ServiceInstanceID(),
	// 	),
	// )
	if err != nil {
		log.Fatal(err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		// sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	tracer = tp.Tracer("beauty")
	return func() {
		tp.Shutdown(context.Background())
		_ = exporter.Shutdown(context.Background())
	}
}

func SpanFromContext(ctx context.Context, spanName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	if tracer == nil {
		tracer = otel.GetTracerProvider().Tracer("beauty")
	}
	return tracer.Start(ctx, spanName, opts...)
}
