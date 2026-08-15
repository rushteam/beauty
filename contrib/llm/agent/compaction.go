package agent

import (
	"fmt"
	"unicode/utf8"

	"github.com/rushteam/beauty/contrib/llm/agent/compaction"
)

// truncateRunes 把 s 截到前 n 个字符,并附省略标记说明省略了多少字符。
// 保留供 compaction_test 与外部引用;实际逻辑在 compaction 子包。
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
	return s[:cut] + fmt.Sprintf("…[compacted: %d/%d runes omitted]", total-n, total)
}

// ToolResultsCompaction 返回 tool 结果压缩策略(原 Compactor 的等价配置)。
func ToolResultsCompaction(maxTokens, keepRecent, toolResultMaxRunes int, estimate func(string) int) compaction.Strategy {
	return &compaction.ToolResults{
		MaxTokens:          maxTokens,
		KeepRecent:         keepRecent,
		ToolResultMaxRunes: toolResultMaxRunes,
		Estimate:           estimate,
	}
}
