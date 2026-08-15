// Package compaction 提供可组合的上下文压缩策略,在模型调用前对消息做确定性投影,
// 降低长对话与工具密集 run 的 token 占用。压缩只作用于发给模型的副本,不改动 Runner 规范历史。
//
// 内置策略:SlidingWindow、Truncation、ToolResults、Summarization;可用 Chain 串联。
// 挂到 Runner.Compaction,或通过 Strategy.Hook() 接入 Hooks.BeforeModel。
package compaction

import (
	"context"

	"github.com/rushteam/beauty/contrib/llm"
)

// Strategy 在模型调用前压缩消息投影。
type Strategy interface {
	Compact(ctx context.Context, messages []llm.Message) ([]llm.Message, error)
}

// StrategyFunc 是 Strategy 的函数适配器。
type StrategyFunc func(ctx context.Context, messages []llm.Message) ([]llm.Message, error)

func (f StrategyFunc) Compact(ctx context.Context, messages []llm.Message) ([]llm.Message, error) {
	return f(ctx, messages)
}

// Chain 按顺序应用多个策略;前一个的输出作为下一个的输入。
func Chain(strategies ...Strategy) Strategy {
	return StrategyFunc(func(ctx context.Context, messages []llm.Message) ([]llm.Message, error) {
		out := messages
		var err error
		for _, s := range strategies {
			if s == nil {
				continue
			}
			out, err = s.Compact(ctx, out)
			if err != nil {
				return nil, err
			}
		}
		return out, nil
	})
}
