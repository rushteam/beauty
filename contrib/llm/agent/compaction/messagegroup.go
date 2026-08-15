package compaction

import (
	"github.com/rushteam/beauty/contrib/llm"
)

// GroupKind 标识一组消息构成的原子分组类型。
type GroupKind int

const (
	GroupSystem    GroupKind = iota // system 消息 — 永不逐出
	GroupUserTurn                   // 用户回合
	GroupAssistant                  // 纯文本 assistant 回复
	GroupToolChain                  // assistant(tool_calls) + tool 结果 — 原子单元
	GroupSummary                    // 注入的摘要 — 替换已逐出的分组
)

// MessageGroup 是一组应一起保留或一起逐出的原子消息。
// 工具调用链(带 ToolCalls 的 assistant + 对应 Tool 消息)构成单一分组,
// 不可只逐出其中一部分,否则会留下孤立的 tool 结果。
type MessageGroup struct {
	Kind     GroupKind
	Messages []llm.Message
	Tokens   int  // 该分组的估算 token 数
	Excluded bool // 策略标记为逐出
}

// MessageIndex 将扁平消息序列组织为原子分组,供安全压缩使用。
type MessageIndex struct {
	Groups   []*MessageGroup
	Estimate func(string) int
}

// NewMessageIndex 从扁平消息切片构建 MessageIndex。分组规则:
//  1. 连续 system 消息 → GroupSystem
//  2. user 消息 → GroupUserTurn
//  3. 带 ToolCalls 的 assistant + 后续匹配的 Tool 消息 → GroupToolChain(原子)
//  4. 无 ToolCalls 的 assistant → GroupAssistant
func NewMessageIndex(msgs []llm.Message, estimate func(string) int) *MessageIndex {
	if estimate == nil {
		estimate = DefaultEstimate
	}
	idx := &MessageIndex{Estimate: estimate}
	i := 0
	for i < len(msgs) {
		m := msgs[i]
		switch {
		case m.Role == llm.System:
			start := i
			for i < len(msgs) && msgs[i].Role == llm.System {
				i++
			}
			idx.addGroup(GroupSystem, msgs[start:i])
		case m.Role == llm.User:
			idx.addGroup(GroupUserTurn, msgs[i:i+1])
			i++
		case m.Role == llm.Assistant && len(m.ToolCalls) > 0:
			start := i
			i++
			want := make(map[string]struct{}, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				want[tc.ID] = struct{}{}
			}
			for i < len(msgs) && msgs[i].Role == llm.Tool {
				if _, ok := want[msgs[i].ToolCallID]; ok {
					i++
					continue
				}
				break
			}
			idx.addGroup(GroupToolChain, msgs[start:i])
		case m.Role == llm.Assistant:
			idx.addGroup(GroupAssistant, msgs[i:i+1])
			i++
		case m.Role == llm.Tool:
			// 孤立 tool 消息(缺少前置 assistant tool_calls),仍作为原子 tool 链处理。
			idx.addGroup(GroupToolChain, msgs[i:i+1])
			i++
		default:
			idx.addGroup(GroupUserTurn, msgs[i:i+1])
			i++
		}
	}
	return idx
}

func (idx *MessageIndex) addGroup(kind GroupKind, msgs []llm.Message) {
	if len(msgs) == 0 {
		return
	}
	tokens := 0
	for _, m := range msgs {
		tokens += MessageTokens(m, idx.Estimate)
	}
	idx.Groups = append(idx.Groups, &MessageGroup{
		Kind:     kind,
		Messages: append([]llm.Message(nil), msgs...),
		Tokens:   tokens,
	})
}

// TotalTokens 返回所有未逐出分组的估算 token 总数。
func (idx *MessageIndex) TotalTokens() int {
	total := 0
	for _, g := range idx.Groups {
		if !g.Excluded {
			total += g.Tokens
		}
	}
	return total
}

// Flatten 返回压缩后的消息切片(已逐出的分组被省略)。
func (idx *MessageIndex) Flatten() []llm.Message {
	out := make([]llm.Message, 0, len(idx.Groups))
	for _, g := range idx.Groups {
		if g.Excluded {
			continue
		}
		out = append(out, g.Messages...)
	}
	return out
}

// ExcludeOldest 将最旧的 n 个可逐出分组标记为 Excluded。
// system 分组与已标记分组会被跳过。
func (idx *MessageIndex) ExcludeOldest(n int) {
	if n <= 0 {
		return
	}
	left := n
	for _, g := range idx.Groups {
		if left == 0 {
			break
		}
		if g.Kind == GroupSystem || g.Excluded {
			continue
		}
		g.Excluded = true
		left--
	}
}

// excludeOldestMatching 逐出最旧的一个满足 predicate 且不在保留尾部的分组。
// protectedTail 表示从末尾起始终保留的分组数。
func (idx *MessageIndex) excludeOldestMatching(protectedTail int, match func(*MessageGroup) bool) bool {
	if protectedTail <= 0 {
		protectedTail = 0
	}
	limit := len(idx.Groups) - protectedTail
	if limit < 0 {
		limit = 0
	}
	for i := 0; i < limit; i++ {
		g := idx.Groups[i]
		if g.Excluded || !match(g) {
			continue
		}
		g.Excluded = true
		return true
	}
	return false
}
