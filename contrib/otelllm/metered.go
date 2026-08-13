package otelllm

import (
	"context"
	"time"

	"github.com/rushteam/beauty/contrib/llm"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// UsageReport 扩展 llm.UsageHook,包含错误信息。
type UsageReport struct {
	Model   string
	Usage   llm.Usage
	Latency time.Duration
	Err     error  // 非 nil 表示调用失败
	Stream  bool   // 是否流式调用
	System  string // AI provider(如 "openai")
}

// UsageReportHook 是增强版用量回调,包含错误信息。
type UsageReportHook func(ctx context.Context, report UsageReport)

// MeteredWithErrors 增强版 Metered,同时上报成功和失败调用,
// 解决原版 llm.Metered 只在成功时回调的局限。
func MeteredWithErrors(c llm.Client, hook UsageReportHook, system string) llm.Client {
	return &meteredErr{c: c, hook: hook, system: system}
}

type meteredErr struct {
	c      llm.Client
	hook   UsageReportHook
	system string
}

func (m *meteredErr) Generate(ctx context.Context, req llm.Request) (*llm.Response, error) {
	start := time.Now()
	resp, err := m.c.Generate(ctx, req)

	report := UsageReport{
		Model:   req.Model,
		Latency: time.Since(start),
		System:  m.system,
		Err:     err,
	}
	if resp != nil {
		report.Model = resp.Model
		report.Usage = resp.Usage
	}
	if m.hook != nil {
		m.hook(ctx, report)
	}
	return resp, err
}

func (m *meteredErr) Stream(ctx context.Context, req llm.Request) (<-chan llm.Chunk, error) {
	start := time.Now()
	src, err := m.c.Stream(ctx, req)
	if err != nil {
		if m.hook != nil {
			m.hook(ctx, UsageReport{
				Model:   req.Model,
				Latency: time.Since(start),
				System:  m.system,
				Stream:  true,
				Err:     err,
			})
		}
		return nil, err
	}
	out := make(chan llm.Chunk)
	go func() {
		defer close(out)
		var usage llm.Usage
		var streamErr error
		for ch := range src {
			if ch.Usage != nil {
				usage = *ch.Usage
			}
			if ch.Err != nil {
				streamErr = ch.Err
			}
			out <- ch
		}
		if m.hook != nil {
			m.hook(ctx, UsageReport{
				Model:   req.Model,
				Usage:   usage,
				Latency: time.Since(start),
				System:  m.system,
				Stream:  true,
				Err:     streamErr,
			})
		}
	}()
	return out, nil
}

// OTelUsageHook 返回一个 UsageReportHook,将用量数据上报到 OTel Metrics。
// 这是一个开箱即用的回调,与 MeteredWithErrors 配合使用。
func OTelUsageHook(opts ...MetricHookOption) UsageReportHook {
	cfg := &metricHookConfig{}
	for _, o := range opts {
		o(cfg)
	}
	mp := cfg.meterProvider
	if mp == nil {
		mp = otel.GetMeterProvider()
	}
	meter := mp.Meter(instrumentationName)

	callCount, _ := meter.Int64Counter("gen_ai.client.calls",
		metric.WithDescription("Total LLM calls (success + error)"))
	errorCount, _ := meter.Int64Counter("gen_ai.client.errors",
		metric.WithDescription("Failed LLM calls"))
	tokenIn, _ := meter.Int64Counter("gen_ai.client.tokens.input",
		metric.WithDescription("Total input tokens"))
	tokenOut, _ := meter.Int64Counter("gen_ai.client.tokens.output",
		metric.WithDescription("Total output tokens"))
	latency, _ := meter.Float64Histogram("gen_ai.client.duration",
		metric.WithDescription("LLM call duration"),
		metric.WithUnit("s"))

	return func(ctx context.Context, r UsageReport) {
		attrs := []attribute.KeyValue{
			attrOperationName.String("chat"),
		}
		if r.System != "" {
			attrs = append(attrs, attrSystem.String(r.System))
		}
		if r.Model != "" {
			attrs = append(attrs, attrRequestModel.String(r.Model))
		}
		if r.Stream {
			attrs = append(attrs, attrStreamMode.Bool(true))
		}

		mopts := metric.WithAttributes(attrs...)
		callCount.Add(ctx, 1, mopts)
		latency.Record(ctx, r.Latency.Seconds(), mopts)

		if r.Err != nil {
			errorCount.Add(ctx, 1, mopts)
		} else {
			tokenIn.Add(ctx, int64(r.Usage.InputTokens), mopts)
			tokenOut.Add(ctx, int64(r.Usage.OutputTokens), mopts)
		}
	}
}

// MetricHookOption 配置 OTelUsageHook。
type MetricHookOption func(*metricHookConfig)

type metricHookConfig struct {
	meterProvider metric.MeterProvider
}

// WithMetricHookMeterProvider 使用自定义 MeterProvider。
func WithMetricHookMeterProvider(mp metric.MeterProvider) MetricHookOption {
	return func(c *metricHookConfig) { c.meterProvider = mp }
}
