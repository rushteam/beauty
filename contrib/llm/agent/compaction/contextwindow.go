package compaction

import (
	"context"

	"github.com/rushteam/beauty/contrib/llm"
)

// ContextWindow 实现模型感知的两阶段逐出策略:
//
//   - 阶段 1(ToolEvictionThreshold,默认 50%): 仅逐出 GroupToolChain 分组
//   - 阶段 2(TruncationThreshold,默认 80%): 逐出任意非 system 分组
//
// 尽可能保留用户回合,同时优先逐出占用上下文最大的旧工具调用链。
type ContextWindow struct {
	MaxInputTokens        int     // 输入 token 总预算
	ToolEvictionThreshold float64 // 0.5 = 阶段 1 目标为预算的 50%
	TruncationThreshold   float64 // 0.8 = 阶段 2 目标为预算的 80%
	KeepRecentGroups      int     // 始终保留的最近分组数(默认 4)
	Estimate              func(string) int
}

func (cw *ContextWindow) maxInputTokens() int {
	if cw.MaxInputTokens > 0 {
		return cw.MaxInputTokens
	}
	return 8000
}

func (cw *ContextWindow) toolEvictionThreshold() float64 {
	if cw.ToolEvictionThreshold > 0 {
		return cw.ToolEvictionThreshold
	}
	return 0.5
}

func (cw *ContextWindow) truncationThreshold() float64 {
	if cw.TruncationThreshold > 0 {
		return cw.TruncationThreshold
	}
	return 0.8
}

func (cw *ContextWindow) keepRecentGroups() int {
	if cw.KeepRecentGroups > 0 {
		return cw.KeepRecentGroups
	}
	return 4
}

func (cw *ContextWindow) estimate(s string) int {
	if cw.Estimate != nil {
		return cw.Estimate(s)
	}
	return DefaultEstimate(s)
}

// CompactGroups 在 MessageIndex 上执行两阶段逐出。
func (cw *ContextWindow) CompactGroups(_ context.Context, idx *MessageIndex) error {
	budget := cw.maxInputTokens()
	if idx.TotalTokens() <= budget {
		return nil
	}

	threshold1 := int(float64(budget) * cw.toolEvictionThreshold())
	for idx.TotalTokens() > threshold1 {
		if !idx.excludeOldestMatching(cw.keepRecentGroups(), func(g *MessageGroup) bool {
			return g.Kind == GroupToolChain
		}) {
			break
		}
	}

	threshold2 := int(float64(budget) * cw.truncationThreshold())
	for idx.TotalTokens() > threshold2 {
		if !idx.excludeOldestMatching(cw.keepRecentGroups(), func(g *MessageGroup) bool {
			return g.Kind != GroupSystem
		}) {
			break
		}
	}

	return nil
}

// Compact 实现 Strategy。
func (cw *ContextWindow) Compact(ctx context.Context, msgs []llm.Message) ([]llm.Message, error) {
	idx := NewMessageIndex(msgs, cw.estimate)
	if idx.TotalTokens() <= cw.maxInputTokens() {
		return msgs, nil
	}
	if err := cw.CompactGroups(ctx, idx); err != nil {
		return nil, err
	}
	return idx.Flatten(), nil
}
