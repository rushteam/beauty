package compaction

import (
	"context"

	"github.com/rushteam/beauty/contrib/llm"
)

// GroupStrategy 在 MessageIndex 层面操作(分组感知)。
type GroupStrategy interface {
	CompactGroups(ctx context.Context, idx *MessageIndex) error
}

// GroupStrategyFunc 是 GroupStrategy 的函数适配器。
type GroupStrategyFunc func(ctx context.Context, idx *MessageIndex) error

func (f GroupStrategyFunc) CompactGroups(ctx context.Context, idx *MessageIndex) error {
	return f(ctx, idx)
}

// Pipeline 在同一 MessageIndex 上顺序应用多个 GroupStrategy,
// 并作为常规 Strategy 挂到 Runner.Compaction。
type Pipeline struct {
	Strategies []GroupStrategy
	Estimate   func(string) int
}

func (p *Pipeline) estimate(s string) int {
	if p.Estimate != nil {
		return p.Estimate(s)
	}
	return DefaultEstimate(s)
}

// CompactGroups 顺序执行各 GroupStrategy。
func (p *Pipeline) CompactGroups(ctx context.Context, idx *MessageIndex) error {
	for _, s := range p.Strategies {
		if s == nil {
			continue
		}
		if err := s.CompactGroups(ctx, idx); err != nil {
			return err
		}
	}
	return nil
}

// Compact 实现 Strategy: 构建 MessageIndex → 跑 Pipeline → Flatten。
func (p *Pipeline) Compact(ctx context.Context, msgs []llm.Message) ([]llm.Message, error) {
	idx := NewMessageIndex(msgs, p.estimate)
	if err := p.CompactGroups(ctx, idx); err != nil {
		return nil, err
	}
	return idx.Flatten(), nil
}
