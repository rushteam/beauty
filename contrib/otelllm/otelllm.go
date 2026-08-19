// Package otelllm 为 contrib/llm 提供可插拔的 OpenTelemetry 可观测性,包括:
//
//   - Trace:为每次 Generate/Stream 创建语义化 Span(遵循 OTel GenAI 约定)
//   - Metrics:LLM 调用计数、token 用量、延迟直方图
//   - AgentHooks:Agent 循环每步自动创建 parent-child span(run tree)
//
// 非侵入设计:所有功能通过装饰 llm.Client 或注入 agent.Hooks 实现,
// 不修改 contrib/llm 核心代码。
//
// 用法:
//
//	// 1. 包装 Client(自动 trace + metrics)
//	cli := otelllm.Instrument(openai.New(key))
//
//	// 2. Agent 级别 span(可选)
//	runner := &agent.Runner{
//	    Client: cli,
//	    Hooks:  otelllm.NewAgentHooks(),
//	}
//
// 导出的 Span 可被 Jaeger/Grafana Tempo/Datadog 直接可视化,
// 也可通过 OTLP Exporter 导出到 Langfuse/LangSmith 等 AI 专用平台。
package otelllm

import (
	"context"
	"fmt"
	"iter"
	"time"

	"github.com/rushteam/beauty/contrib/llm"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const (
	instrumentationName = "github.com/rushteam/beauty/contrib/otelllm"
)

// GenAI semantic convention attribute keys (OpenTelemetry GenAI semconv).
var (
	attrSystem          = attribute.Key("gen_ai.system")
	attrRequestModel    = attribute.Key("gen_ai.request.model")
	attrResponseModel   = attribute.Key("gen_ai.response.model")
	attrFinishReason    = attribute.Key("gen_ai.response.finish_reasons")
	attrInputTokens     = attribute.Key("gen_ai.usage.input_tokens")
	attrOutputTokens    = attribute.Key("gen_ai.usage.output_tokens")
	attrMaxTokens       = attribute.Key("gen_ai.request.max_tokens")
	attrTemperature     = attribute.Key("gen_ai.request.temperature")
	attrOperationName   = attribute.Key("gen_ai.operation.name")
	attrToolCallCount   = attribute.Key("gen_ai.tool_call.count")
	attrCacheInputWrite = attribute.Key("gen_ai.usage.cache_creation_input_tokens")
	attrCacheInputRead  = attribute.Key("gen_ai.usage.cache_read_input_tokens")
	attrStreamMode      = attribute.Key("gen_ai.stream")
)

// Option 配置 Instrument。
type Option func(*config)

type config struct {
	system         string
	tracerProvider trace.TracerProvider
	meterProvider  metric.MeterProvider
	recordPrompt   bool // 是否记录 prompt 内容到 span(默认关闭,避免泄露敏感数据)
}

// WithSystem 标注 AI provider 名(如 "openai", "anthropic")。
// 不设置时从 Response.Model 推断。
func WithSystem(system string) Option {
	return func(c *config) { c.system = system }
}

// WithTracerProvider 使用自定义 TracerProvider(默认用全局)。
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(c *config) { c.tracerProvider = tp }
}

// WithMeterProvider 使用自定义 MeterProvider(默认用全局)。
func WithMeterProvider(mp metric.MeterProvider) Option {
	return func(c *config) { c.meterProvider = mp }
}

// WithRecordPrompt 开启后将 system prompt 和最后一条 user 消息记录到 span events。
// 注意:可能包含敏感信息,生产环境谨慎开启。
func WithRecordPrompt() Option {
	return func(c *config) { c.recordPrompt = true }
}

// Instrument 包装一个 llm.Client,自动为每次调用创建 OTel Span 并记录 Metrics。
func Instrument(c llm.Client, opts ...Option) llm.Client {
	cfg := &config{}
	for _, o := range opts {
		o(cfg)
	}
	if cfg.tracerProvider == nil {
		cfg.tracerProvider = otel.GetTracerProvider()
	}
	if cfg.meterProvider == nil {
		cfg.meterProvider = otel.GetMeterProvider()
	}

	tracer := cfg.tracerProvider.Tracer(instrumentationName)
	meter := cfg.meterProvider.Meter(instrumentationName)

	// 初始化 metrics
	callCount, _ := meter.Int64Counter("gen_ai.client.operation.duration.count",
		metric.WithDescription("Number of LLM calls"))
	tokenInput, _ := meter.Int64Counter("gen_ai.client.token.usage.input",
		metric.WithDescription("Input tokens consumed"))
	tokenOutput, _ := meter.Int64Counter("gen_ai.client.token.usage.output",
		metric.WithDescription("Output tokens consumed"))
	latencyHist, _ := meter.Float64Histogram("gen_ai.client.operation.duration",
		metric.WithDescription("LLM call duration in seconds"),
		metric.WithUnit("s"))
	errorCount, _ := meter.Int64Counter("gen_ai.client.error.count",
		metric.WithDescription("Number of failed LLM calls"))

	return &instrumentedClient{
		inner:       c,
		cfg:         cfg,
		tracer:      tracer,
		callCount:   callCount,
		tokenInput:  tokenInput,
		tokenOutput: tokenOutput,
		latencyHist: latencyHist,
		errorCount:  errorCount,
	}
}

type instrumentedClient struct {
	inner       llm.Client
	cfg         *config
	tracer      trace.Tracer
	callCount   metric.Int64Counter
	tokenInput  metric.Int64Counter
	tokenOutput metric.Int64Counter
	latencyHist metric.Float64Histogram
	errorCount  metric.Int64Counter
}

func (ic *instrumentedClient) Generate(ctx context.Context, req llm.Request) (*llm.Response, error) {
	spanName := "chat"
	if req.Model != "" {
		spanName = fmt.Sprintf("chat %s", req.Model)
	}

	ctx, span := ic.tracer.Start(ctx, spanName,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(ic.requestAttrs(req, false)...),
	)
	defer span.End()

	if ic.cfg.recordPrompt {
		ic.recordPromptEvents(span, req)
	}

	start := time.Now()
	resp, err := ic.inner.Generate(ctx, req)
	duration := time.Since(start)

	metricAttrs := ic.metricAttrs(req)

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		ic.errorCount.Add(ctx, 1, metric.WithAttributes(metricAttrs...))
		ic.callCount.Add(ctx, 1, metric.WithAttributes(metricAttrs...))
		ic.latencyHist.Record(ctx, duration.Seconds(), metric.WithAttributes(metricAttrs...))
		return nil, err
	}

	span.SetAttributes(ic.responseAttrs(resp)...)
	ic.callCount.Add(ctx, 1, metric.WithAttributes(metricAttrs...))
	ic.tokenInput.Add(ctx, int64(resp.Usage.InputTokens), metric.WithAttributes(metricAttrs...))
	ic.tokenOutput.Add(ctx, int64(resp.Usage.OutputTokens), metric.WithAttributes(metricAttrs...))
	ic.latencyHist.Record(ctx, duration.Seconds(), metric.WithAttributes(metricAttrs...))

	return resp, nil
}

func (ic *instrumentedClient) Stream(ctx context.Context, req llm.Request) iter.Seq2[llm.Chunk, error] {
	return func(yield func(llm.Chunk, error) bool) {
		spanName := "chat"
		if req.Model != "" {
			spanName = fmt.Sprintf("chat %s", req.Model)
		}

		ctx, span := ic.tracer.Start(ctx, spanName,
			trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(ic.requestAttrs(req, true)...),
		)
		defer span.End()

		if ic.cfg.recordPrompt {
			ic.recordPromptEvents(span, req)
		}

		start := time.Now()
		var usage llm.Usage
		var model string
		var toolCallCount int
		var streamErr error

		for ch, err := range ic.inner.Stream(ctx, req) {
			if err != nil {
				streamErr = err
				yield(ch, err)
				break
			}
			if ch.Usage != nil {
				usage = *ch.Usage
			}
			if len(ch.ToolCalls) > 0 {
				toolCallCount = len(ch.ToolCalls)
			}
			if !yield(ch, nil) {
				break
			}
		}

		duration := time.Since(start)
		metricAttrs := ic.metricAttrs(req)

		if model == "" {
			model = req.Model
		}

		if streamErr != nil {
			span.RecordError(streamErr)
			span.SetStatus(codes.Error, streamErr.Error())
			ic.errorCount.Add(ctx, 1, metric.WithAttributes(metricAttrs...))
		} else {
			span.SetAttributes(
				attrResponseModel.String(model),
				attrInputTokens.Int(usage.InputTokens),
				attrOutputTokens.Int(usage.OutputTokens),
			)
			if toolCallCount > 0 {
				span.SetAttributes(attrToolCallCount.Int(toolCallCount))
			}
			if usage.CacheCreationInputTokens > 0 {
				span.SetAttributes(attrCacheInputWrite.Int(usage.CacheCreationInputTokens))
			}
			if usage.CacheReadInputTokens > 0 {
				span.SetAttributes(attrCacheInputRead.Int(usage.CacheReadInputTokens))
			}
			ic.tokenInput.Add(ctx, int64(usage.InputTokens), metric.WithAttributes(metricAttrs...))
			ic.tokenOutput.Add(ctx, int64(usage.OutputTokens), metric.WithAttributes(metricAttrs...))
		}

		ic.callCount.Add(ctx, 1, metric.WithAttributes(metricAttrs...))
		ic.latencyHist.Record(ctx, duration.Seconds(), metric.WithAttributes(metricAttrs...))
	}
}

func (ic *instrumentedClient) requestAttrs(req llm.Request, stream bool) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attrOperationName.String("chat"),
		attrStreamMode.Bool(stream),
	}
	if ic.cfg.system != "" {
		attrs = append(attrs, attrSystem.String(ic.cfg.system))
	}
	if req.Model != "" {
		attrs = append(attrs, attrRequestModel.String(req.Model))
	}
	if req.MaxTokens > 0 {
		attrs = append(attrs, attrMaxTokens.Int(req.MaxTokens))
	}
	if req.Temperature > 0 {
		attrs = append(attrs, attrTemperature.Float64(req.Temperature))
	}
	return attrs
}

func (ic *instrumentedClient) responseAttrs(resp *llm.Response) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attrResponseModel.String(resp.Model),
		attrInputTokens.Int(resp.Usage.InputTokens),
		attrOutputTokens.Int(resp.Usage.OutputTokens),
	}
	if resp.StopReason != "" {
		attrs = append(attrs, attrFinishReason.StringSlice([]string{resp.StopReason}))
	}
	if len(resp.ToolCalls) > 0 {
		attrs = append(attrs, attrToolCallCount.Int(len(resp.ToolCalls)))
	}
	if resp.Usage.CacheCreationInputTokens > 0 {
		attrs = append(attrs, attrCacheInputWrite.Int(resp.Usage.CacheCreationInputTokens))
	}
	if resp.Usage.CacheReadInputTokens > 0 {
		attrs = append(attrs, attrCacheInputRead.Int(resp.Usage.CacheReadInputTokens))
	}
	return attrs
}

func (ic *instrumentedClient) metricAttrs(req llm.Request) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attrOperationName.String("chat"),
	}
	if ic.cfg.system != "" {
		attrs = append(attrs, attrSystem.String(ic.cfg.system))
	}
	if req.Model != "" {
		attrs = append(attrs, attrRequestModel.String(req.Model))
	}
	return attrs
}

func (ic *instrumentedClient) recordPromptEvents(span trace.Span, req llm.Request) {
	if req.System != "" {
		span.AddEvent("gen_ai.system.message",
			trace.WithAttributes(attribute.String("gen_ai.prompt", req.System)))
	}
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == llm.User {
			span.AddEvent("gen_ai.user.message",
				trace.WithAttributes(attribute.String("gen_ai.prompt", req.Messages[i].Content)))
			break
		}
	}
}
