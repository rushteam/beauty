package otelmq

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/rushteam/beauty/pkg/messaging/mq"
)

// Trace 返回 HandlerMiddleware:从 Message.Headers 提取 trace context,并开启
// consumer process span。对齐 franz-go kotel 的 WithProcessSpan,但挂在 mq.Chain
// 上,与 Recover/Retry 组合使用。
//
//	handler := mq.Chain(business, otelmq.Trace("order"), mq.Recover())
//
// tracerName 作为 instrumentation scope 后缀(如服务/领域名),便于在后端区分。
func Trace(tracerName string, opts ...Option) mq.HandlerMiddleware {
	if tracerName == "" {
		tracerName = "mq"
	}
	cfg := applyOpts(opts...)
	tracer := cfg.tp.Tracer(instrumentationName + "/" + tracerName)
	return func(next mq.Handler) mq.Handler {
		return func(ctx context.Context, msg mq.Message) error {
			return runProcess(ctx, msg, next, tracer, cfg.prop, cfg.system, cfg.spanLink)
		}
	}
}

func runProcess(
	ctx context.Context,
	msg mq.Message,
	next mq.Handler,
	tracer trace.Tracer,
	prop propagation.TextMapPropagator,
	system string,
	spanLink bool,
) error {
	if len(msg.Headers) > 0 {
		ctx = prop.Extract(ctx, headersCarrier(msg.Headers))
	}

	attrs := []attribute.KeyValue{
		semconv.MessagingSystemKey.String(system),
		semconv.MessagingDestinationName(msg.Topic),
		semconv.MessagingOperationName("process"),
		semconv.MessagingOperationTypeProcess,
	}
	if msg.Key != "" {
		attrs = append(attrs, attribute.String("messaging.message.key", msg.Key))
	}

	opts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(attrs...),
	}
	if spanLink {
		opts = append(opts, trace.WithNewRoot())
		if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
			opts = append(opts, trace.WithLinks(trace.Link{SpanContext: sc}))
		}
	}

	ctx, span := tracer.Start(ctx, msg.Topic+" process", opts...)
	defer span.End()

	err := next(ctx, msg)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return err
}
