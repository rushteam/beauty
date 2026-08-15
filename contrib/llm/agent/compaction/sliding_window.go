package compaction

import (
	"context"

	"github.com/rushteam/beauty/contrib/llm"
)

// SlidingWindow 只保留最近 MaxMessages 条消息。
// PreserveSystem 为 true 时,若首条为 system 角色则始终保留(不计入窗口)。
type SlidingWindow struct {
	MaxMessages    int
	PreserveSystem bool
}

// Compact 实现 Strategy。
func (w *SlidingWindow) Compact(_ context.Context, msgs []llm.Message) ([]llm.Message, error) {
	max := w.MaxMessages
	if max <= 0 {
		max = 20
	}
	if len(msgs) <= max {
		return msgs, nil
	}

	if w.PreserveSystem && len(msgs) > 0 && msgs[0].Role == llm.System {
		// 保留首条 system,窗口只作用于后续消息。
		if len(msgs)-1 <= max {
			return msgs, nil
		}
		keep := max
		if keep < 1 {
			keep = 1
		}
		tail := msgs[len(msgs)-keep:]
		out := make([]llm.Message, 0, 1+len(tail))
		out = append(out, msgs[0])
		out = append(out, tail...)
		return out, nil
	}

	return append([]llm.Message{}, msgs[len(msgs)-max:]...), nil
}
