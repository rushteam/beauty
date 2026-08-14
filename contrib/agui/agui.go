// Package agui 桥接 AG-UI 协议与 beauty 的 agent 体系。
//
// AG-UI 是一套轻量 SSE 流式协议,定义了 Agent 与前端之间的标准事件交互方式。
// 核心概念:
//   - RunAgentInput: 客户端请求(thread/run ID + 消息 + 工具声明 + 状态)
//   - Event: SSE 事件(RUN_STARTED/TEXT_MESSAGE_*/TOOL_CALL_*/RUN_FINISHED 等)
//
// 本包提供:
//   - Server: 将 beauty agent.StreamAgent 暴露为 AG-UI HTTP 端点(POST → SSE)
//   - Client: 将远程 AG-UI 服务包装为 beauty agent.Agent
//
// 协议规范: https://github.com/ag-ui-protocol/ag-ui
package agui

import (
	"encoding/json"
	"time"

	"github.com/rushteam/beauty/contrib/llm"
)

// ──────────────────────────────────────────────────────────────────────────────
// AG-UI 协议事件类型
// ──────────────────────────────────────────────────────────────────────────────

// EventType 定义 AG-UI 协议事件类型。
type EventType string

const (
	EventRunStarted   EventType = "RUN_STARTED"
	EventRunFinished  EventType = "RUN_FINISHED"
	EventRunError     EventType = "RUN_ERROR"
	EventStepStarted  EventType = "STEP_STARTED"
	EventStepFinished EventType = "STEP_FINISHED"

	EventTextMessageStart   EventType = "TEXT_MESSAGE_START"
	EventTextMessageContent EventType = "TEXT_MESSAGE_CONTENT"
	EventTextMessageEnd     EventType = "TEXT_MESSAGE_END"

	EventToolCallStart  EventType = "TOOL_CALL_START"
	EventToolCallArgs   EventType = "TOOL_CALL_ARGS"
	EventToolCallEnd    EventType = "TOOL_CALL_END"
	EventToolCallResult EventType = "TOOL_CALL_RESULT"

	EventReasoningMessageStart   EventType = "REASONING_MESSAGE_START"
	EventReasoningMessageContent EventType = "REASONING_MESSAGE_CONTENT"
	EventReasoningMessageEnd     EventType = "REASONING_MESSAGE_END"

	EventStateSnapshot EventType = "STATE_SNAPSHOT"
	EventStateDelta    EventType = "STATE_DELTA"
)

// Event 是 AG-UI 协议的 SSE 事件。
type Event struct {
	Type      EventType `json:"type"`
	ThreadID  string    `json:"threadId,omitempty"`
	RunID     string    `json:"runId,omitempty"`
	Timestamp int64     `json:"timestamp,omitempty"`

	// TEXT_MESSAGE_* 字段
	MessageID string `json:"messageId,omitempty"`
	Role      string `json:"role,omitempty"`
	Delta     string `json:"delta,omitempty"`

	// TOOL_CALL_* 字段
	ToolCallID   string `json:"toolCallId,omitempty"`
	ToolCallName string `json:"toolCallName,omitempty"`

	// RUN_ERROR 字段
	Message   string `json:"message,omitempty"`
	ErrorCode string `json:"code,omitempty"`

	// STEP_* 字段
	StepName string `json:"stepName,omitempty"`

	// STATE_SNAPSHOT / STATE_DELTA 字段
	Snapshot json.RawMessage `json:"snapshot,omitempty"`

	// TOOL_CALL_RESULT 字段
	Content string `json:"content,omitempty"`

	// RUN_FINISHED 字段
	Result string `json:"result,omitempty"`
}

// ──────────────────────────────────────────────────────────────────────────────
// AG-UI 请求模型
// ──────────────────────────────────────────────────────────────────────────────

// RunAgentInput 是 AG-UI 客户端发给服务端的请求体。
type RunAgentInput struct {
	ThreadID string          `json:"threadId,omitempty"`
	RunID    string          `json:"runId,omitempty"`
	Messages []InputMessage  `json:"messages,omitempty"`
	Tools    []InputTool     `json:"tools,omitempty"`
	State    json.RawMessage `json:"state,omitempty"`
	Context  json.RawMessage `json:"context,omitempty"`
}

// InputMessage 是 AG-UI 协议的消息格式。
type InputMessage struct {
	ID         string          `json:"id,omitempty"`
	Role       string          `json:"role"`
	Content    string          `json:"content,omitempty"`
	ToolCalls  []InputToolCall `json:"toolCalls,omitempty"`
	ToolCallID string          `json:"toolCallId,omitempty"`
}

// InputToolCall 是 AG-UI 协议的工具调用格式。
type InputToolCall struct {
	ID       string        `json:"id"`
	Type     string        `json:"type"`
	Function InputFunction `json:"function"`
}

// InputFunction 是 AG-UI 工具调用中的函数定义。
type InputFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// InputTool 是 AG-UI 协议的工具声明格式。
type InputTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// ──────────────────────────────────────────────────────────────────────────────
// 类型转换
// ──────────────────────────────────────────────────────────────────────────────

// inputToBeautyMessages 将 AG-UI 输入消息转为 beauty llm.Message。
func inputToBeautyMessages(input *RunAgentInput) []llm.Message {
	var messages []llm.Message
	for _, m := range input.Messages {
		msg := llm.Message{
			Role:    aguiRoleToBeauty(m.Role),
			Content: m.Content,
		}
		if m.ToolCallID != "" {
			msg.ToolCallID = m.ToolCallID
		}
		for _, tc := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, llm.ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: json.RawMessage(tc.Function.Arguments),
			})
		}
		messages = append(messages, msg)
	}
	return messages
}

// inputToBeautyTools 将 AG-UI 工具声明转为 beauty llm.ToolDef(仅声明,不可执行)。
func inputToBeautyTools(tools []InputTool) []llm.ToolDef {
	var defs []llm.ToolDef
	for _, t := range tools {
		defs = append(defs, llm.ToolDef{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		})
	}
	return defs
}

func aguiRoleToBeauty(role string) llm.Role {
	switch role {
	case "system":
		return llm.System
	case "assistant":
		return llm.Assistant
	case "tool":
		return llm.Tool
	default:
		return llm.User
	}
}

func beautyRoleToAGUI(role llm.Role) string {
	switch role {
	case llm.System:
		return "system"
	case llm.Assistant:
		return "assistant"
	case llm.Tool:
		return "tool"
	default:
		return "user"
	}
}

func nowTimestamp() int64 {
	return time.Now().UnixMilli()
}
