package otelmq

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/rushteam/beauty/pkg/mq"
)

const instrumentationName = "github.com/rushteam/beauty/pkg/mq/otelmq"

// Publisher 装饰 mq.Publisher:开 producer span,并把当前 trace context 注入
// Message.Headers(W3C traceparent/tracestate)。语义对齐 franz-go kotel 的
// publish 钩子,但基于 Headers map,与具体 broker 无关(Kafka/NATS/InProc 均可)。
//
//	pub := otelmq.Publisher(kafka.NewPublisher(brokers))
func Publisher(inner mq.Publisher, opts ...Option) mq.Publisher {
	if inner == nil {
		return nil
	}
	cfg := applyOpts(opts...)
	return &otelPublisher{
		inner:  inner,
		tracer: cfg.tp.Tracer(instrumentationName),
		prop:   cfg.prop,
		system: cfg.system,
	}
}

type otelPublisher struct {
	inner  mq.Publisher
	tracer trace.Tracer
	prop   propagation.TextMapPropagator
	system string
}

func (p *otelPublisher) Publish(ctx context.Context, msg mq.Message) error {
	msg.Headers = cloneHeaders(msg.Headers)

	attrs := []attribute.KeyValue{
		semconv.MessagingSystemKey.String(p.system),
		semconv.MessagingDestinationName(msg.Topic),
		semconv.MessagingOperationName("publish"),
		semconv.MessagingOperationTypeSend,
	}
	if msg.Key != "" {
		attrs = append(attrs, attribute.String("messaging.message.key", msg.Key))
	}

	ctx, span := p.tracer.Start(ctx, msg.Topic+" publish",
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(attrs...),
	)
	defer span.End()

	p.prop.Inject(ctx, headersCarrier(msg.Headers))

	err := p.inner.Publish(ctx, msg)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return err
}
