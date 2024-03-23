package tracing

import (
	"context"
	"log"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

var tracer trace.Tracer

func NewTracing() func() {
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
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
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

// func newExporter() (sdktrace.SpanExporter, error) {
// 	return stdouttrace.New()
// }
