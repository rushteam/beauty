package agent

import (
	"context"
	"iter"

	"github.com/rushteam/beauty/contrib/llm"
)

// ---- Agent 级中间件 ----
//
// AgentMiddleware 包装 AgentRunFunc,在 agent 运行流程外层叠加横切关注点
// (日志、evaluator loop、mode switching、OTel trace 等)。
// 与 Provider 级(llm.Client 装饰器)分层:
//   - Provider 级:Fallback/Retry/Guard/Budget/Cache — 每次模型调用
//   - Agent 级:在 history/context 注入之后、整轮 agent loop 前后生效
//
// 链式组合,outer → inner 包裹;最内层是 agent 的 runLoop。

// AgentRunFunc 是 agent 运行的核心签名。
type AgentRunFunc func(ctx context.Context, req llm.Request, opts ...Option) iter.Seq2[Event, error]

// AgentMiddleware 包装 AgentRunFunc。next 是内层(更靠近真实 agent loop);
// 返回的新 AgentRunFunc 可在调用 next 前后加逻辑。
type AgentMiddleware func(next AgentRunFunc) AgentRunFunc

// ChainMiddleware 把多个中间件从外到内链式组合。middlewares[0] 在最外层。
func ChainMiddleware(middlewares ...AgentMiddleware) AgentMiddleware {
	return func(next AgentRunFunc) AgentRunFunc {
		for i := len(middlewares) - 1; i >= 0; i-- {
			next = middlewares[i](next)
		}
		return next
	}
}

// ---- 内置 Agent 中间件 ----

// LoggingMiddleware 在运行前后记录请求/响应摘要。
func LoggingMiddleware(logFn func(ctx context.Context, event string, data map[string]any)) AgentMiddleware {
	return func(next AgentRunFunc) AgentRunFunc {
		return func(ctx context.Context, req llm.Request, opts ...Option) iter.Seq2[Event, error] {
			if logFn != nil {
				logFn(ctx, "agent.run.start", map[string]any{
					"model":    req.Model,
					"messages": len(req.Messages),
					"tools":    len(req.Tools),
				})
			}
			return func(yield func(Event, error) bool) {
				var lastResp *llm.Response
				for ev, err := range next(ctx, req, opts...) {
					if err != nil {
						if logFn != nil {
							logFn(ctx, "agent.run.error", map[string]any{"error": err.Error()})
						}
						yield(ev, err)
						return
					}
					if ev.Type == EventFinal {
						lastResp = ev.Response
					}
					if !yield(ev, nil) {
						return
					}
				}
				if logFn != nil && lastResp != nil {
					logFn(ctx, "agent.run.done", map[string]any{
						"content_len":   len(lastResp.Content),
						"input_tokens":  lastResp.Usage.InputTokens,
						"output_tokens": lastResp.Usage.OutputTokens,
					})
				}
			}
		}
	}
}

// EvaluatorMiddleware 在 agent 运行后用 evaluator 评估结果,不达标则带反馈重跑(最多 maxRetries 次)。
func EvaluatorMiddleware(evaluate func(ctx context.Context, resp *llm.Response) (ok bool, feedback string, err error), maxRetries int) AgentMiddleware {
	if maxRetries <= 0 {
		maxRetries = 3
	}
	return func(next AgentRunFunc) AgentRunFunc {
		return func(ctx context.Context, req llm.Request, opts ...Option) iter.Seq2[Event, error] {
			return func(yield func(Event, error) bool) {
				currentReq := req
				for attempt := range maxRetries + 1 {
					_ = attempt
					var lastResp *llm.Response
					for ev, err := range next(ctx, currentReq, opts...) {
						if err != nil {
							yield(ev, err)
							return
						}
						if ev.Type == EventFinal {
							lastResp = ev.Response
						}
						if ev.Type != EventFinal {
							if !yield(ev, nil) {
								return
							}
						}
					}
					if lastResp == nil || evaluate == nil {
						yield(Event{Type: EventFinal, Response: lastResp}, nil)
						return
					}
					ok, feedback, err := evaluate(ctx, lastResp)
					if err != nil {
						yield(Event{}, err)
						return
					}
					if ok {
						yield(Event{Type: EventFinal, Response: lastResp}, nil)
						return
					}
					currentReq.Messages = append(currentReq.Messages,
						llm.Message{Role: llm.Assistant, Content: lastResp.Content, Source: llm.SourceModel},
						llm.Message{Role: llm.User, Content: feedback, Source: llm.SourceMiddleware},
					)
				}
			}
		}
	}
}

// SourceAttributionMiddleware 自动给 agent 产生的消息标记来源。
func SourceAttributionMiddleware(agentName string) AgentMiddleware {
	return func(next AgentRunFunc) AgentRunFunc {
		return func(ctx context.Context, req llm.Request, opts ...Option) iter.Seq2[Event, error] {
			return func(yield func(Event, error) bool) {
				for ev, err := range next(ctx, req, opts...) {
					if err != nil {
						yield(ev, err)
						return
					}
					if ev.AgentName == "" {
						ev.AgentName = agentName
					}
					if !yield(ev, nil) {
						return
					}
				}
			}
		}
	}
}
