package otelllm

import (
	"context"
	"fmt"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// NewAgentHooks 创建一个 agent.Hooks 实例,为 Agent 循环的每步自动创建 OTel Span:
//
//   - BeforeModel/AfterModel: 为每次模型调用创建 "agent.model step=N" span
//   - BeforeTool/AfterTool: 为每次工具调用创建 "agent.tool {name}" span
//
// 生成的 span 树结构(父 span 由调用方在 ctx 中创建):
//
//	[HTTP handler / agent.run]
//	  ├─ [agent.model step=1]  tokens_in=500 tokens_out=80
//	  ├─ [agent.tool query_order step=1]  duration=120ms
//	  ├─ [agent.model step=2]  tokens_in=650 tokens_out=120
//	  └─ [agent.tool send_email step=2]  duration=300ms
func NewAgentHooks(opts ...AgentHookOption) agent.Hooks {
	cfg := &agentHookConfig{}
	for _, o := range opts {
		o(cfg)
	}
	tp := cfg.tracerProvider
	if tp == nil {
		tp = otel.GetTracerProvider()
	}
	tracer := tp.Tracer(instrumentationName)

	return agent.Hooks{
		BeforeModel: func(ctx context.Context, step int, req *llm.Request) error {
			spanName := fmt.Sprintf("agent.model step=%d", step)
			if req.Model != "" {
				spanName = fmt.Sprintf("agent.model %s step=%d", req.Model, step)
			}
			_, span := tracer.Start(ctx, spanName,
				trace.WithSpanKind(trace.SpanKindInternal),
				trace.WithAttributes(
					attribute.Int("agent.step", step),
					attrOperationName.String("chat"),
				),
			)
			if req.Model != "" {
				span.SetAttributes(attrRequestModel.String(req.Model))
			}
			if len(req.Tools) > 0 {
				span.SetAttributes(attribute.Int("gen_ai.request.tool_count", len(req.Tools)))
			}
			storeModelSpan(step, span)
			return nil
		},
		AfterModel: func(ctx context.Context, step int, resp *llm.Response) error {
			span := loadModelSpan(step)
			if span == nil {
				return nil
			}
			defer span.End()

			span.SetAttributes(
				attrResponseModel.String(resp.Model),
				attrInputTokens.Int(resp.Usage.InputTokens),
				attrOutputTokens.Int(resp.Usage.OutputTokens),
			)
			if resp.StopReason != "" {
				span.SetAttributes(attrFinishReason.StringSlice([]string{resp.StopReason}))
			}
			if len(resp.ToolCalls) > 0 {
				span.SetAttributes(attrToolCallCount.Int(len(resp.ToolCalls)))
			}
			return nil
		},
		BeforeTool: func(ctx context.Context, step int, tc *llm.ToolCall) (agent.Permission, error) {
			spanName := fmt.Sprintf("agent.tool %s", tc.Name)
			_, span := tracer.Start(ctx, spanName,
				trace.WithSpanKind(trace.SpanKindInternal),
				trace.WithAttributes(
					attribute.Int("agent.step", step),
					attribute.String("gen_ai.tool.name", tc.Name),
					attribute.String("gen_ai.tool.call_id", tc.ID),
				),
			)
			storeToolSpan(step, tc.ID, span)
			return agent.PermitAllow, nil
		},
		AfterTool: func(ctx context.Context, step int, tc llm.ToolCall, result *string) error {
			span := loadToolSpan(step, tc.ID)
			if span == nil {
				return nil
			}
			defer span.End()

			r := ""
			if result != nil {
				r = *result
			}
			if len(r) > 1024 {
				span.SetAttributes(attribute.Int("gen_ai.tool.result_length", len(r)))
			}
			if r == "" {
				span.SetAttributes(attribute.String("gen_ai.tool.result_status", "empty"))
			}
			return nil
		},
	}
}

// AgentHookOption 配置 NewAgentHooks。
type AgentHookOption func(*agentHookConfig)

type agentHookConfig struct {
	tracerProvider trace.TracerProvider
}

// WithHookTracerProvider 使用自定义 TracerProvider。
func WithHookTracerProvider(tp trace.TracerProvider) AgentHookOption {
	return func(c *agentHookConfig) { c.tracerProvider = tp }
}

// MergeHooks 合并两个 Hooks。委托给 agent.MergeHooks。
func MergeHooks(a, b agent.Hooks) agent.Hooks {
	return agent.MergeHooks(a, b)
}

// RecordRunOutcome 在 span 上记录 Agent 运行结果(token 总量、步数、状态)。
func RecordRunOutcome(span trace.Span, outcome agent.RunOutcome) {
	span.SetAttributes(
		attribute.String("agent.run_id", outcome.RunID),
		attribute.String("agent.status", string(outcome.Status)),
	)
	if outcome.Response != nil {
		span.SetAttributes(
			attrInputTokens.Int(outcome.Response.Usage.InputTokens),
			attrOutputTokens.Int(outcome.Response.Usage.OutputTokens),
		)
	}
	if outcome.Err != nil {
		span.RecordError(outcome.Err)
		span.SetStatus(codes.Error, outcome.Err.Error())
	}
}
