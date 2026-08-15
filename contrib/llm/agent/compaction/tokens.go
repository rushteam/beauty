package compaction

import (
	"unicode/utf8"

	"github.com/rushteam/beauty/contrib/llm"
)

// DefaultEstimate 是默认 token 估算器(约 4 字节/token)。
func DefaultEstimate(s string) int {
	return len(s)/4 + 1
}

// MessageTokens 估算一条消息的 token 数。
func MessageTokens(m llm.Message, estimate func(string) int) int {
	if estimate == nil {
		estimate = DefaultEstimate
	}
	n := estimate(m.Content)
	for _, p := range m.Parts {
		n += estimate(p.Text)
	}
	return n
}

// TotalTokens 估算消息序列的总 token 数。
func TotalTokens(msgs []llm.Message, estimate func(string) int) int {
	total := 0
	for _, m := range msgs {
		total += MessageTokens(m, estimate)
	}
	return total
}

// truncateRunes 把 s 截到前 n 个字符,并附省略标记。
func truncateRunes(s string, n int) string {
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
	return s[:cut] + "…[compacted]"
}
