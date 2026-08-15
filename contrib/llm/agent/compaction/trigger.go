package compaction

import (
	"context"

	"github.com/rushteam/beauty/contrib/llm"
)

// Trigger 是一个谓词:判断当前消息是否需要压缩。
// 返回 true 时 Strategy 才会被执行(按需触发,避免不必要的遍历)。
type Trigger func(messages []llm.Message) bool

// TokensExceed 当消息估算 token 数超过 maxTokens 时触发。
func TokensExceed(maxTokens int) Trigger {
	return func(messages []llm.Message) bool {
		return TotalTokens(messages, DefaultEstimate) > maxTokens
	}
}

// MessagesExceed 当消息条数超过 maxMessages 时触发。
func MessagesExceed(maxMessages int) Trigger {
	return func(messages []llm.Message) bool {
		return len(messages) > maxMessages
	}
}

// TurnsExceed 当 user 轮数超过 maxTurns 时触发。
func TurnsExceed(maxTurns int) Trigger {
	return func(messages []llm.Message) bool {
		turns := 0
		for _, m := range messages {
			if m.Role == llm.User {
				turns++
			}
		}
		return turns > maxTurns
	}
}

// HasToolCalls 当消息中存在工具调用时触发。
func HasToolCalls() Trigger {
	return func(messages []llm.Message) bool {
		for _, m := range messages {
			if len(m.ToolCalls) > 0 {
				return true
			}
		}
		return false
	}
}

// All 所有 trigger 都满足时才触发。
func All(triggers ...Trigger) Trigger {
	return func(messages []llm.Message) bool {
		for _, t := range triggers {
			if !t(messages) {
				return false
			}
		}
		return true
	}
}

// Any 任一 trigger 满足即触发。
func Any(triggers ...Trigger) Trigger {
	return func(messages []llm.Message) bool {
		for _, t := range triggers {
			if t(messages) {
				return true
			}
		}
		return false
	}
}

// Triggered 包装一个 Strategy,仅在 trigger 满足时执行压缩;否则原样返回。
func Triggered(trigger Trigger, strategy Strategy) Strategy {
	return StrategyFunc(func(ctx context.Context, messages []llm.Message) ([]llm.Message, error) {
		if !trigger(messages) {
			return messages, nil
		}
		return strategy.Compact(ctx, messages)
	})
}
