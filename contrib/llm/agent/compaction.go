package agent

import (
	"fmt"
	"unicode/utf8"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent/compaction"
)

// ==== 运行内上下文压缩:Compactor ====
//
// 工具密集的长跑里,历史 tool 结果消息会不断累积、撑爆上下文。Compactor 在**每轮模型调用前**
// 对将要发出的消息做一次确定性压缩:估算总 token,超阈值时从最旧的、过大的 tool 结果开始截断,
// 直到降到阈值以下或没有可截断的了。末尾 KeepRecent 条消息完整保留(最近上下文最重要)。
//
// 与 session 子包的滚动摘要不同:摘要是**跨轮**、要调模型生成概要;Compactor 是**单轮内**、
// 纯 token 计数 + 截断,**不调用模型**,零额外延迟与费用。二者可叠加。
//
// 更多策略见子包 agent/compaction(SlidingWindow、Truncation、Summarization、Chain)。
// Runner.Compaction 可挂任意 Strategy;Compactor 保留为 ToolResults 的便捷别名。
//
// 关键:压缩只作用于发给模型的**投影**副本,Runner 内部的规范历史(msgs)保持完整——因此每轮都
// 基于完整历史重新投影,不会永久丢信息。只截断 Role=Tool 的消息(通常最大且最可截),不动对话消息。
//
// 机制而非策略:阈值、保留窗口、单条上限、token 估算器都可调,默认给一组温和值。

// Compactor 配置运行内 tool 结果压缩。挂到 Runner.Compactor(nil=不启用)。
// 等价于 compaction.ToolResults,保留以兼容旧代码。
type Compactor struct {
	// MaxTokens 是触发压缩的估算 token 阈值:整个消息序列估算超过它才压缩。<=0 用默认 4000。
	MaxTokens int
	// KeepRecent 是末尾完整保留、不参与压缩的消息条数。<=0 用默认 6。
	KeepRecent int
	// ToolResultMaxRunes 是被压缩的单条 tool 结果保留的最大字符数(其后加省略标记)。<=0 用默认 256。
	ToolResultMaxRunes int
	// Estimate 估算一段文本的 token 数。nil 用默认(约 4 字节/token 的粗略估算)。
	Estimate func(string) int
}

func (c *Compactor) strategy() *compaction.ToolResults {
	if c == nil {
		return nil
	}
	return &compaction.ToolResults{
		MaxTokens:          c.MaxTokens,
		KeepRecent:         c.KeepRecent,
		ToolResultMaxRunes: c.ToolResultMaxRunes,
		Estimate:           c.Estimate,
	}
}

// Project 返回压缩后的消息投影。若无需压缩则原样返回入参切片(不复制);否则返回新切片,
// 只截断最旧的、超过单条上限的 tool 结果,直到估算总量降至阈值以下或无更多可截。入参不被改动。
// Runner.Compactor 每轮自动调用它;也可单独调用以在别处复用同一压缩逻辑。
func (c *Compactor) Project(msgs []llm.Message) []llm.Message {
	if c == nil {
		return msgs
	}
	out, err := c.strategy().Compact(nil, msgs)
	if err != nil || out == nil {
		return msgs
	}
	return out
}

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
