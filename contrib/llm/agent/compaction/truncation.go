package compaction

import (
	"context"
	"unicode/utf8"

	"github.com/rushteam/beauty/contrib/llm"
)

// Truncation 在总 token 超 MaxTokens 时,从最旧的消息开始截断 Content,
// 末尾 KeepRecent 条完整保留。比 ToolResults 更激进——所有角色都可截断。
type Truncation struct {
	MaxTokens  int
	KeepRecent int
	MaxRunes   int // 单条消息截断上限;<=0 用 512
	Estimate   func(string) int
}

func (t *Truncation) maxTokens() int {
	if t.MaxTokens > 0 {
		return t.MaxTokens
	}
	return 4000
}

func (t *Truncation) keepRecent() int {
	if t.KeepRecent > 0 {
		return t.KeepRecent
	}
	return 6
}

func (t *Truncation) maxRunes() int {
	if t.MaxRunes > 0 {
		return t.MaxRunes
	}
	return 512
}

func (t *Truncation) estimate(s string) int {
	if t.Estimate != nil {
		return t.Estimate(s)
	}
	return DefaultEstimate(s)
}

// Compact 实现 Strategy。
func (t *Truncation) Compact(_ context.Context, msgs []llm.Message) ([]llm.Message, error) {
	total := TotalTokens(msgs, t.estimate)
	if total <= t.maxTokens() {
		return msgs, nil
	}

	limit := len(msgs) - t.keepRecent()
	maxRunes := t.maxRunes()
	var out []llm.Message
	for i := range limit {
		m := msgs[i]
		if total <= t.maxTokens() {
			break
		}
		if utf8.RuneCountInString(m.Content) <= maxRunes {
			continue
		}
		if out == nil {
			out = append(out, msgs...)
		}
		before := MessageTokens(m, t.estimate)
		truncated := m
		truncated.Content = truncateRunes(m.Content, maxRunes)
		out[i] = truncated
		total += MessageTokens(truncated, t.estimate) - before
	}
	if out == nil {
		return msgs, nil
	}
	return out, nil
}
