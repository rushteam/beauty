package agent

import (
	"context"
	"iter"
	"time"

	"github.com/rushteam/beauty/contrib/llm"
)

// ---- OTel 可观测性中间件 ----
//
// 遵循 OpenTelemetry GenAI 语义约定(gen_ai.*)的 agent 中间件。
// 本包不直接依赖 OTel SDK——通过回调接口注入,由使用方桥接到
// beauty 核心的 WithTrace/WithMetric 或直接用 OTel API。
// 这保持了 contrib/llm 零外部依赖的原则。

// SpanKind 标识 span 类型。
type SpanKind int

const (
	SpanAgent    SpanKind = iota // agent 整轮运行
	SpanModel                    // 单次模型调用
	SpanTool                     // 单次工具执行
	SpanWorkflow                 // 工作流节点
)

// Span 是一次可观测操作的句柄。
type Span interface {
	// SetAttribute 设置键值属性。
	SetAttribute(key string, value any)
	// RecordError 记录错误。
	RecordError(err error)
	// End 结束 span,传入最终状态。
	End()
}

// Tracer 创建和管理 span。使用方注入实现以桥接到实际的 tracing 后端。
type Tracer interface {
	// Start 创建并开始一个新 span。返回带 span 的 context 和 span 句柄。
	Start(ctx context.Context, name string, kind SpanKind) (context.Context, Span)
}

// MetricRecorder 记录度量指标。
type MetricRecorder interface {
	// RecordDuration 记录操作耗时。
	RecordDuration(ctx context.Context, name string, duration time.Duration, attrs map[string]any)
	// RecordTokens 记录 token 使用量。
	RecordTokens(ctx context.Context, model string, usage llm.Usage, attrs map[string]any)
	// RecordToolCall 记录工具调用。
	RecordToolCall(ctx context.Context, toolName string, duration time.Duration, err error)
}

// OTelConfig 可观测性配置。
type OTelConfig struct {
	Tracer  Tracer
	Metrics MetricRecorder
}

// OTelMiddleware 创建 agent 级可观测性中间件。
// 在 agent 运行的关键路径上埋点:
//   - agent.run: 整轮运行的 span + 耗时
//   - agent.model: 每次模型调用的 span + token 用量
//   - agent.tool: 每次工具执行的 span + 耗时
func OTelMiddleware(cfg OTelConfig) AgentMiddleware {
	return func(next AgentRunFunc) AgentRunFunc {
		return func(ctx context.Context, req llm.Request, opts ...Option) iter.Seq2[Event, error] {
			return func(yield func(Event, error) bool) {
				start := time.Now()
				if cfg.Tracer != nil {
					var span Span
					ctx, span = cfg.Tracer.Start(ctx, "agent.run", SpanAgent)
					span.SetAttribute("gen_ai.system", "beauty")
					span.SetAttribute("gen_ai.request.model", req.Model)
					span.SetAttribute("gen_ai.request.max_tokens", req.MaxTokens)
					span.SetAttribute("gen_ai.request.temperature", req.Temperature)
					span.SetAttribute("gen_ai.tools.count", len(req.Tools))
					defer span.End()
				}

				var totalUsage llm.Usage
				var stepCount int
				var toolCount int

				for ev, err := range next(ctx, req, opts...) {
					if err != nil {
						if cfg.Tracer != nil {
							// span 会在 defer 中 End
						}
						yield(ev, err)
						return
					}

					switch ev.Type {
					case EventStep:
						stepCount++
						if ev.Response != nil {
							totalUsage.InputTokens += ev.Response.Usage.InputTokens
							totalUsage.OutputTokens += ev.Response.Usage.OutputTokens
							if cfg.Metrics != nil {
								cfg.Metrics.RecordTokens(ctx, req.Model, ev.Response.Usage, map[string]any{
									"gen_ai.agent.name": ev.AgentName,
									"gen_ai.step":       ev.Step,
								})
							}
						}

					case EventToolStart:
						toolCount++
						if cfg.Tracer != nil && ev.ToolCall != nil {
							_, toolSpan := cfg.Tracer.Start(ctx, "agent.tool."+ev.ToolCall.Name, SpanTool)
							toolSpan.SetAttribute("gen_ai.tool.name", ev.ToolCall.Name)
							toolSpan.SetAttribute("gen_ai.tool.call_id", ev.ToolCall.ID)
							toolSpan.End()
						}

					case EventToolResult:
						if cfg.Metrics != nil && ev.ToolCall != nil {
							cfg.Metrics.RecordToolCall(ctx, ev.ToolCall.Name, 0, nil)
						}
					}

					if !yield(ev, nil) {
						return
					}
				}

				elapsed := time.Since(start)
				if cfg.Metrics != nil {
					cfg.Metrics.RecordDuration(ctx, "agent.run.duration", elapsed, map[string]any{
						"gen_ai.request.model": req.Model,
						"gen_ai.steps":         stepCount,
						"gen_ai.tools.calls":   toolCount,
					})
					cfg.Metrics.RecordTokens(ctx, req.Model, totalUsage, map[string]any{
						"gen_ai.scope": "total",
					})
				}
			}
		}
	}
}

// OTelClientMiddleware 是 llm.Client 级的可观测性装饰器。
// 在每次模型调用上创建 span 并记录 token 用量。
func OTelClientMiddleware(c llm.Client, cfg OTelConfig) llm.Client {
	return &otelClient{c: c, cfg: cfg}
}

type otelClient struct {
	c   llm.Client
	cfg OTelConfig
}

func (o *otelClient) Generate(ctx context.Context, req llm.Request) (*llm.Response, error) {
	start := time.Now()
	if o.cfg.Tracer != nil {
		var span Span
		ctx, span = o.cfg.Tracer.Start(ctx, "gen_ai.generate", SpanModel)
		span.SetAttribute("gen_ai.request.model", req.Model)
		span.SetAttribute("gen_ai.request.max_tokens", req.MaxTokens)
		defer span.End()
	}

	resp, err := o.c.Generate(ctx, req)
	elapsed := time.Since(start)

	if err != nil {
		if o.cfg.Metrics != nil {
			o.cfg.Metrics.RecordDuration(ctx, "gen_ai.generate.duration", elapsed, map[string]any{
				"gen_ai.request.model": req.Model,
				"error":                true,
			})
		}
		return nil, err
	}

	if o.cfg.Metrics != nil {
		o.cfg.Metrics.RecordDuration(ctx, "gen_ai.generate.duration", elapsed, map[string]any{
			"gen_ai.request.model":  req.Model,
			"gen_ai.response.model": resp.Model,
		})
		o.cfg.Metrics.RecordTokens(ctx, resp.Model, resp.Usage, nil)
	}
	return resp, nil
}

func (o *otelClient) Stream(ctx context.Context, req llm.Request) iter.Seq2[llm.Chunk, error] {
	start := time.Now()
	if o.cfg.Tracer != nil {
		var span Span
		ctx, span = o.cfg.Tracer.Start(ctx, "gen_ai.stream", SpanModel)
		span.SetAttribute("gen_ai.request.model", req.Model)
		defer span.End()
	}

	return func(yield func(llm.Chunk, error) bool) {
		var usage llm.Usage
		for c, err := range o.c.Stream(ctx, req) {
			if err != nil {
				if o.cfg.Metrics != nil {
					o.cfg.Metrics.RecordDuration(ctx, "gen_ai.stream.duration", time.Since(start), map[string]any{
						"gen_ai.request.model": req.Model,
						"error":                true,
					})
				}
				yield(c, err)
				return
			}
			if c.Usage != nil {
				usage = *c.Usage
			}
			if !yield(c, nil) {
				return
			}
		}
		elapsed := time.Since(start)
		if o.cfg.Metrics != nil {
			o.cfg.Metrics.RecordDuration(ctx, "gen_ai.stream.duration", elapsed, map[string]any{
				"gen_ai.request.model": req.Model,
			})
			o.cfg.Metrics.RecordTokens(ctx, req.Model, usage, nil)
		}
	}
}

// ---- NoOp 实现(测试 / 禁用时使用)----

// NoOpTracer 不做任何追踪。
type NoOpTracer struct{}

func (NoOpTracer) Start(ctx context.Context, _ string, _ SpanKind) (context.Context, Span) {
	return ctx, noOpSpan{}
}

type noOpSpan struct{}

func (noOpSpan) SetAttribute(string, any) {}
func (noOpSpan) RecordError(error)        {}
func (noOpSpan) End()                     {}

// NoOpMetrics 不做任何度量记录。
type NoOpMetrics struct{}

func (NoOpMetrics) RecordDuration(context.Context, string, time.Duration, map[string]any) {}
func (NoOpMetrics) RecordTokens(context.Context, string, llm.Usage, map[string]any)       {}
func (NoOpMetrics) RecordToolCall(context.Context, string, time.Duration, error)          {}
