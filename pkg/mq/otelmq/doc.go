// Package otelmq 为 pkg/mq 提供 OpenTelemetry trace 透传(opt-in)。
//
// 背景:gRPC 已默认挂 otelgrpc,同步调用链天然贯通;异步 MQ 链路则需把
// W3C TraceContext 写入 Message.Headers,消费端再 Extract。本包提供:
//
//   - Publisher:装饰 mq.Publisher,开 producer span 并 Inject 到 Headers
//   - Trace:HandlerMiddleware,Extract Headers 并开 consumer process span
//
// 设计取舍:
//
//   - 放在 pkg/mq/otelmq 子包(类似 otelhttp),保持 pkg/mq 零外部依赖、默认不启用。
//   - 基于 map[string]string Headers,与 broker 无关——InProc / contrib/kafka
//     (segmentio) / contrib/nats 都受益。
//   - 不使用 franz-go 的 kotel:beauty 的 contrib/kafka 基于 segmentio/kafka-go,
//     而非 franz-go;kotel 绑 kgo.Hook,无法复用。本包在语义上对齐 kotel 的
//     publish / process span(messaging semconv + SpanKind),只是载体换成
//     mq.Message.Headers。
//
// 用法:
//
//	pub := otelmq.Publisher(kafka.NewPublisher(brokers))
//	h := mq.Chain(business, otelmq.Trace("order"), mq.Recover())
//	consumer := mq.NewConsumer(sub).Handle("trade.success", h, mq.WithGroup("g"))
package otelmq
