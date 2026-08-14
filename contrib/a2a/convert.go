// Package a2a 桥接 A2A (Agent-to-Agent) 协议与 beauty 的 agent 体系,
// 让 beauty agent 可以调用远程 A2A agent(客户端),也可以把自身暴露为 A2A 服务(服务端)。
//
// A2A 协议核心概念:Task(有状态会话单元)、Message(一次对话轮)、Part(内容块)、
// Artifact(增量输出)。通过 TaskState 管理生命周期:
// Submitted → Working → Completed/Failed/InputRequired。
//
// 本包作为"胶水"模块,同时依赖 a2a-go SDK 与 beauty/contrib/llm/agent,
// 让两者保持零耦合。
package a2a

import (
	"encoding/json"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/rushteam/beauty/contrib/llm"
)

// ──────────────────────────────────────────────────────────────────────────────
// beauty Message → A2A Parts
// ──────────────────────────────────────────────────────────────────────────────

// messagesToParts 将 beauty 消息列表转为 A2A 内容块。
func messagesToParts(messages []llm.Message) []*a2a.Part {
	var parts []*a2a.Part
	for _, m := range messages {
		if m.Content != "" {
			parts = append(parts, a2a.NewTextPart(m.Content))
		}
		for _, p := range m.Parts {
			switch p.Type {
			case llm.PartText:
				if p.Text != "" {
					parts = append(parts, a2a.NewTextPart(p.Text))
				}
			case llm.PartImage:
				if p.ImageURL != "" {
					parts = append(parts, a2a.NewFileURLPart(a2a.URL(p.ImageURL), "image/*"))
				}
			}
		}
		for _, tc := range m.ToolCalls {
			b, _ := json.Marshal(tc)
			parts = append(parts, a2a.NewTextPart(string(b)))
		}
	}
	return parts
}

// ──────────────────────────────────────────────────────────────────────────────
// A2A Parts → beauty Message
// ──────────────────────────────────────────────────────────────────────────────

// partsToMessage 将 A2A 内容块列表转为单条 beauty Message。
func partsToMessage(parts a2a.ContentParts, role llm.Role) llm.Message {
	var texts []string
	var multimodal []llm.Part

	for _, p := range parts {
		switch {
		case p.Text() != "":
			texts = append(texts, p.Text())
		case p.URL() != "":
			multimodal = append(multimodal, llm.Part{
				Type:     llm.PartImage,
				ImageURL: string(p.URL()),
			})
		case p.Data() != nil:
			b, _ := json.Marshal(p.Data())
			texts = append(texts, string(b))
		case len(p.Raw()) > 0:
			texts = append(texts, string(p.Raw()))
		}
	}

	msg := llm.Message{Role: role}
	if len(multimodal) > 0 {
		for _, t := range texts {
			multimodal = append([]llm.Part{{Type: llm.PartText, Text: t}}, multimodal...)
		}
		msg.Parts = multimodal
	} else {
		msg.Content = strings.Join(texts, "\n")
	}
	return msg
}

// a2aRoleToBeauty 将 A2A 角色映射到 beauty 角色。
func a2aRoleToBeauty(role a2a.MessageRole) llm.Role {
	switch role {
	case a2a.MessageRoleAgent:
		return llm.Assistant
	default:
		return llm.User
	}
}

// textFromParts 从 ContentParts 提取纯文本。
func textFromParts(parts a2a.ContentParts) string {
	var sb strings.Builder
	for _, p := range parts {
		if t := p.Text(); t != "" {
			sb.WriteString(t)
		}
	}
	return sb.String()
}
