package compaction

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/rushteam/beauty/contrib/llm"
)

// Snip 截断过大的 tool 结果:保留头+尾,中间用标记替换。
// 只改 Role=Tool 的消息,不碰对话正文。
type Snip struct {
	// MaxRunes 超过此 rune 数才截断。<=0 视为 8000。
	MaxRunes int
	// PrefixRunes 保留的头部。<=0 视为 MaxRunes 的一半。
	PrefixRunes int
	// SuffixRunes 保留的尾部。<=0 视为 MaxRunes 的四分之一。
	SuffixRunes int
}

// DefaultSnip 返回默认头尾截断策略。
func DefaultSnip() *Snip {
	return &Snip{MaxRunes: 8000, PrefixRunes: 4000, SuffixRunes: 2000}
}

func (s *Snip) maxRunes() int {
	if s != nil && s.MaxRunes > 0 {
		return s.MaxRunes
	}
	return 8000
}

func (s *Snip) prefix() int {
	if s != nil && s.PrefixRunes > 0 {
		return s.PrefixRunes
	}
	return s.maxRunes() / 2
}

func (s *Snip) suffix() int {
	if s != nil && s.SuffixRunes > 0 {
		return s.SuffixRunes
	}
	return s.maxRunes() / 4
}

// Compact 实现 Strategy。
func (s *Snip) Compact(_ context.Context, msgs []llm.Message) ([]llm.Message, error) {
	max := s.maxRunes()
	pre, suf := s.prefix(), s.suffix()
	var out []llm.Message
	for i, m := range msgs {
		if m.Role != llm.Tool {
			continue
		}
		n := utf8.RuneCountInString(m.Content)
		if n <= max {
			continue
		}
		if out == nil {
			out = append(out, msgs...)
		}
		out[i] = m
		out[i].Content = snipHeadTail(m.Content, pre, suf)
	}
	if out == nil {
		return msgs, nil
	}
	return out, nil
}

func snipHeadTail(s string, prefix, suffix int) string {
	total := utf8.RuneCountInString(s)
	if prefix+suffix >= total {
		return s
	}
	head := cutRunes(s, prefix)
	tail := lastRunes(s, suffix)
	omitted := total - prefix - suffix
	return head + fmt.Sprintf("\n…[snip: %d runes omitted]…\n", omitted) + tail
}

func cutRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	cnt, cut := 0, len(s)
	for idx := range s {
		if cnt == n {
			cut = idx
			break
		}
		cnt++
	}
	return s[:cut]
}

func lastRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	total := utf8.RuneCountInString(s)
	if n >= total {
		return s
	}
	skip := total - n
	cnt, cut := 0, 0
	for idx := range s {
		if cnt == skip {
			cut = idx
			break
		}
		cnt++
	}
	return s[cut:]
}
