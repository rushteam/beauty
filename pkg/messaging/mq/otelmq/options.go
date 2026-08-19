package otelmq

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Option 配置 otelmq 组件(Publisher / Trace)。未设置时使用全局 TracerProvider
// 与 TextMapPropagator——与 beauty.WithTrace() 配置的传播器一致。
type Option func(*config)

type config struct {
	tp       trace.TracerProvider
	prop     propagation.TextMapPropagator
	system   string // messaging.system;空则 "mq"(broker 无关)
	spanLink bool   // consumer 用 Link 连父 span,而非 child(对齐 kotel LinkSpans)
}

func applyOpts(opts ...Option) config {
	cfg := config{}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.tp == nil {
		cfg.tp = otel.GetTracerProvider()
	}
	if cfg.prop == nil {
		cfg.prop = otel.GetTextMapPropagator()
	}
	if cfg.system == "" {
		cfg.system = "mq"
	}
	return cfg
}

// WithTracerProvider 覆盖全局 TracerProvider。
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(c *config) { c.tp = tp }
}

// WithPropagator 覆盖全局 TextMapPropagator。
func WithPropagator(p propagation.TextMapPropagator) Option {
	return func(c *config) { c.prop = p }
}

// WithMessagingSystem 设置 messaging.system(默认 "mq")。若确定是 Kafka/NATS
// 可设为 "kafka" / "nats",便于后端按系统过滤。
func WithMessagingSystem(system string) Option {
	return func(c *config) {
		if system != "" {
			c.system = system
		}
	}
}

// WithSpanLinks 让消费端 process span 以 Link 关联发布端 span,而非父子关系
// (对齐 franz-go kotel 的 LinkSpans;适合异步解耦场景)。
func WithSpanLinks() Option {
	return func(c *config) { c.spanLink = true }
}
