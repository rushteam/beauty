package compaction

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/rushteam/beauty/contrib/llm"
)

// ToolResults 压缩历史 tool 结果:超 token 阈值时从最旧的大 tool 消息开始截断,
// 末尾 KeepRecent 条完整保留。与 agent.Compactor 行为一致。
type ToolResults struct {
	MaxTokens          int
	KeepRecent         int
	ToolResultMaxRunes int
	Estimate           func(string) int
}

func (c *ToolResults) maxTokens() int {
	if c.MaxTokens > 0 {
		return c.MaxTokens
	}
	return 4000
}

func (c *ToolResults) keepRecent() int {
	if c.KeepRecent > 0 {
		return c.KeepRecent
	}
	return 6
}

func (c *ToolResults) toolResultMaxRunes() int {
	if c.ToolResultMaxRunes > 0 {
		return c.ToolResultMaxRunes
	}
	return 256
}

func (c *ToolResults) estimate(s string) int {
	if c.Estimate != nil {
		return c.Estimate(s)
	}
	return DefaultEstimate(s)
}

// Compact 实现 Strategy。
func (c *ToolResults) Compact(_ context.Context, msgs []llm.Message) ([]llm.Message, error) {
	total := TotalTokens(msgs, c.estimate)
	if total <= c.maxTokens() {
		return msgs, nil
	}

	limit := len(msgs) - c.keepRecent()
	maxRunes := c.toolResultMaxRunes()
	var out []llm.Message
	for i := range limit {
		m := msgs[i]
		if total <= c.maxTokens() {
			break
		}
		if m.Role != llm.Tool || utf8.RuneCountInString(m.Content) <= maxRunes {
			continue
		}
		if out == nil {
			out = append(out, msgs...)
		}
		before := MessageTokens(m, c.estimate)
		truncated := m
		truncated.Content = truncateRunesWithDetail(m.Content, maxRunes)
		out[i] = truncated
		total += MessageTokens(truncated, c.estimate) - before
	}
	if out == nil {
		return msgs, nil
	}
	return out, nil
}

func truncateRunesWithDetail(s string, n int) string {
	total := utf8.RuneCountInString(s)
	if total <= n {
		return s
	}
	cnt, cut := 0, len(s)
	for idx := range s {
		if cnt == n {
			cut = idx
			break
		}
		cnt++
	}
	return s[:cut] + fmt.Sprintf("…[compacted: %d/%d runes omitted]", total-n, total)
}
