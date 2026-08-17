package compaction

import (
	"context"

	"github.com/rushteam/beauty/contrib/llm"
)

const compactedToolPlaceholder = "…[compacted tool result]"

// Microcompact 丢掉较早的 tool 结果正文,只保留最近 KeepRecent 条完整 tool 消息。
// 用于自动阶梯的第二级:Snip 之后仍超阈值时再砍旧结果。
type Microcompact struct {
	// KeepRecent 完整保留的最近 tool 结果条数。<=0 视为 4。
	KeepRecent int
}

func (m *Microcompact) keepRecent() int {
	if m != nil && m.KeepRecent > 0 {
		return m.KeepRecent
	}
	return 4
}

// Compact 实现 Strategy。
func (m *Microcompact) Compact(_ context.Context, msgs []llm.Message) ([]llm.Message, error) {
	keep := m.keepRecent()
	var toolIdx []int
	for i, msg := range msgs {
		if msg.Role == llm.Tool {
			toolIdx = append(toolIdx, i)
		}
	}
	if len(toolIdx) <= keep {
		return msgs, nil
	}
	cut := len(toolIdx) - keep
	out := append([]llm.Message(nil), msgs...)
	for _, i := range toolIdx[:cut] {
		if out[i].Content == compactedToolPlaceholder {
			continue
		}
		out[i].Content = compactedToolPlaceholder
	}
	return out, nil
}
