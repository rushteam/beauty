package compaction

import (
	"context"
	"fmt"
	"strings"

	"github.com/rushteam/beauty/contrib/llm"
)

// SummarizeFunc 把一批历史消息压缩成摘要文本。
type SummarizeFunc func(ctx context.Context, messages []llm.Message) (string, error)

// Summarization 在消息数或 token 超阈值时,把较早的消息折叠成一条摘要消息,
// 只保留最近 KeepRecent 条。适合长跑对话在单次 run 内的投影压缩。
type Summarization struct {
	MaxMessages int
	MaxTokens   int // 与 MaxMessages 二选一触发;两者都设则任一满足即触发
	KeepRecent  int
	Summarize   SummarizeFunc
	Estimate    func(string) int
}

func (s *Summarization) keepRecent() int {
	if s.KeepRecent > 0 {
		return s.KeepRecent
	}
	return 6
}

func (s *Summarization) shouldCompact(msgs []llm.Message) bool {
	if s.MaxMessages > 0 && len(msgs) > s.MaxMessages {
		return true
	}
	if s.MaxTokens > 0 && TotalTokens(msgs, s.Estimate) > s.MaxTokens {
		return true
	}
	return false
}

// Compact 实现 Strategy。
func (s *Summarization) Compact(ctx context.Context, msgs []llm.Message) ([]llm.Message, error) {
	if s.Summarize == nil || !s.shouldCompact(msgs) {
		return msgs, nil
	}

	keep := s.keepRecent()
	if len(msgs) <= keep {
		return msgs, nil
	}

	cut := len(msgs) - keep
	older := msgs[:cut]
	recent := msgs[cut:]

	summary, err := s.Summarize(ctx, older)
	if err != nil {
		return nil, fmt.Errorf("compaction summarization: %w", err)
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return msgs, nil
	}

	out := make([]llm.Message, 0, 1+len(recent))
	out = append(out, llm.Message{
		Role:    llm.System,
		Content: "以下是此前对话的压缩摘要:\n" + summary,
	})
	out = append(out, recent...)
	return out, nil
}

// LLMSummarize 返回基于 llm.Client 的 SummarizeFunc。
func LLMSummarize(client llm.Client, model string) SummarizeFunc {
	return func(ctx context.Context, messages []llm.Message) (string, error) {
		var b strings.Builder
		for _, msg := range messages {
			if msg.Content == "" {
				continue
			}
			b.WriteString(string(msg.Role))
			b.WriteString(": ")
			b.WriteString(msg.Content)
			b.WriteByte('\n')
		}
		resp, err := client.Generate(ctx, llm.Request{
			Model: model,
			System: "你是对话摘要器。把下面的对话压缩成简洁、信息完整的中文摘要," +
				"保留关键事实、决定与未决事项。只输出摘要本身。",
			Messages: []llm.Message{{Role: llm.User, Content: b.String()}},
		})
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(resp.Content), nil
	}
}
