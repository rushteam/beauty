package compaction

import (
	"context"

	"github.com/rushteam/beauty/contrib/llm"
)

// CompactLevel 是上下文占用档位,供告警/自动压缩触发。
type CompactLevel int

const (
	CompactOK       CompactLevel = iota // 低于自动压缩线
	CompactAuto                         // 达到自动压缩阈值
	CompactWarning                      // 约 80%
	CompactError                        // 约 90%
	CompactBlocking                     // 约 95%
)

// WindowState 是一次占用评估的快照。
type WindowState struct {
	Level  CompactLevel
	Tokens int
	Max    int
}

// Auto 是三级自动压缩阶梯:Snip → Microcompact → Summarization。
// 按 WindowState 逐步加码,避免一上来就摘要。
//
// 阈值相对 MaxInputTokens:
//
//	Auto 70% / Warning 80% / Error 90% / Blocking 95%
type Auto struct {
	MaxInputTokens int
	Snip           *Snip
	Micro          *Microcompact
	Summarize      *Summarization
	Estimate       func(string) int
	// OnState 在每次 Compact 时回调当前档位(可选,用于日志/指标)。
	OnState func(WindowState)
}

// AutoLadder 构造默认阶梯。summarize 可为 nil(则第三级跳过)。
func AutoLadder(maxInputTokens int, summarize SummarizeFunc) *Auto {
	if maxInputTokens <= 0 {
		maxInputTokens = 128000
	}
	var sum *Summarization
	if summarize != nil {
		sum = &Summarization{
			MaxTokens:  maxInputTokens * 7 / 10,
			KeepRecent: 6,
			Summarize:  summarize,
		}
	}
	return &Auto{
		MaxInputTokens: maxInputTokens,
		Snip:           DefaultSnip(),
		Micro:          &Microcompact{KeepRecent: 4},
		Summarize:      sum,
	}
}

func (a *Auto) max() int {
	if a != nil && a.MaxInputTokens > 0 {
		return a.MaxInputTokens
	}
	return 128000
}

func (a *Auto) estimate(s string) int {
	if a != nil && a.Estimate != nil {
		return a.Estimate(s)
	}
	return DefaultEstimate(s)
}

// Assess 估算当前占用档位,不修改消息。
func (a *Auto) Assess(msgs []llm.Message) WindowState {
	max := a.max()
	tokens := TotalTokens(msgs, a.estimate)
	st := WindowState{Tokens: tokens, Max: max, Level: CompactOK}
	switch {
	case tokens >= max*95/100:
		st.Level = CompactBlocking
	case tokens >= max*90/100:
		st.Level = CompactError
	case tokens >= max*80/100:
		st.Level = CompactWarning
	case tokens >= max*70/100:
		st.Level = CompactAuto
	}
	return st
}

// Compact 实现 Strategy:按档位执行阶梯。OK 档不改消息。
func (a *Auto) Compact(ctx context.Context, msgs []llm.Message) ([]llm.Message, error) {
	st := a.Assess(msgs)
	if a.OnState != nil {
		a.OnState(st)
	}
	if st.Level == CompactOK {
		return msgs, nil
	}

	out := msgs
	var err error
	if a.Snip != nil {
		out, err = a.Snip.Compact(ctx, out)
		if err != nil {
			return nil, err
		}
	}
	st = a.Assess(out)
	if st.Level <= CompactAuto {
		return out, nil
	}

	if a.Micro != nil && st.Level >= CompactWarning {
		out, err = a.Micro.Compact(ctx, out)
		if err != nil {
			return nil, err
		}
		st = a.Assess(out)
	}
	if a.Summarize != nil && st.Level >= CompactError {
		out, err = a.Summarize.Compact(ctx, out)
		if err != nil {
			return nil, err
		}
	}
	if a.OnState != nil {
		a.OnState(a.Assess(out))
	}
	return out, nil
}
