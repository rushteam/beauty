// Package instruct 提供本地模型的 chat template 格式化:把结构化的 llm.Request(System +
// Messages)编码成各模型家族期望的 instruct prompt 文本(ChatML、Llama 3、Mistral 等)。
//
// 两种使用场景:
//
//  1. Completion 端点:某些本地部署只暴露 /v1/completions(text completion),需要客户端手动拼 prompt。
//  2. Chat 端点但模板不对:服务端内置 chat template 跟模型训练格式不匹配,客户端侧需要覆盖。
//
// 用法:
//
//	// 配合 openai.WithCompletionMode 走 /v1/completions 端点
//	cli := openai.New(key,
//	    openai.WithBaseURL("http://localhost:8080/v1"),
//	    openai.WithCompletionMode(&instruct.Llama3),
//	)
//
//	// 或直接格式化(自行发 HTTP)
//	prompt := instruct.Llama3.Format(req)
package instruct

import (
	"strings"

	"github.com/rushteam/beauty/contrib/llm"
)

// Template 定义如何把结构化对话转为模型期望的 prompt 格式。
// 每个角色(system/user/assistant)由一对 Prefix+Suffix 包裹;
// BOS/EOS 分别加在整个序列的首尾。Format 最后追加 AssistantPrefix 以引导模型生成。
type Template struct {
	Name            string
	BOS             string // 序列开始标记(如 "<|begin_of_text|>")
	EOS             string // 序列结束标记(格式化时不使用,仅供参考)
	SystemPrefix    string
	SystemSuffix    string
	UserPrefix      string
	UserSuffix      string
	AssistantPrefix string
	AssistantSuffix string
	StopStrings     []string // 模板自带的 stop 序列,应合并到请求的 Stop 字段
}

// Format 把 llm.Request 格式化为单一 prompt 字符串。
// 处理顺序:BOS → System → Messages(按顺序) → 尾部 AssistantPrefix(引导生成)。
// Tool 角色消息被跳过(completions 端点不支持工具调用)。
func (t *Template) Format(req llm.Request) string {
	var b strings.Builder
	b.Grow(256)

	if t.BOS != "" {
		b.WriteString(t.BOS)
	}
	if req.System != "" {
		b.WriteString(t.SystemPrefix)
		b.WriteString(req.System)
		b.WriteString(t.SystemSuffix)
	}
	for _, m := range req.Messages {
		text := messageText(m)
		switch m.Role {
		case llm.System:
			b.WriteString(t.SystemPrefix)
			b.WriteString(text)
			b.WriteString(t.SystemSuffix)
		case llm.User:
			b.WriteString(t.UserPrefix)
			b.WriteString(text)
			b.WriteString(t.UserSuffix)
		case llm.Assistant:
			b.WriteString(t.AssistantPrefix)
			b.WriteString(text)
			b.WriteString(t.AssistantSuffix)
		}
	}
	b.WriteString(t.AssistantPrefix)
	return b.String()
}

// MergeStops 将模板自带的 StopStrings 与用户指定的 stops 合并(去重)。
func (t *Template) MergeStops(stops []string) []string {
	if len(t.StopStrings) == 0 {
		return stops
	}
	if len(stops) == 0 {
		out := make([]string, len(t.StopStrings))
		copy(out, t.StopStrings)
		return out
	}
	seen := make(map[string]struct{}, len(stops)+len(t.StopStrings))
	out := make([]string, 0, len(stops)+len(t.StopStrings))
	for _, s := range stops {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	for _, s := range t.StopStrings {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

func messageText(m llm.Message) string {
	if m.Content != "" {
		return m.Content
	}
	if len(m.Parts) == 0 {
		return ""
	}
	var texts []string
	for _, p := range m.Parts {
		if p.Type == llm.PartText && p.Text != "" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n")
}
