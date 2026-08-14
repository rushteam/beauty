// Package checkpoint 提供 Agent 运行的标准 UI 事件 schema、事件日志与 checkpoint 恢复。
//
// 设计目标:
//   - Human-in-the-loop UI: 固定 JSON schema (beauty.agent.v1),可直接 SSE/WebSocket 推送
//   - Sub-agent 编排可视化: parent_run_id + depth + agent.spawned/completed
//   - Checkpoint 恢复: append-only 事件日志 + RunSnapshot.EventCount → Replay 重建 Messages
package checkpoint

import (
	"time"

	"github.com/rushteam/beauty/contrib/llm"
)

// SchemaVersion 是当前 UI 事件协议版本。
const SchemaVersion = "beauty.agent.v1"

// Type 是标准 UI 事件类型(HITL + 编排可视化)。
type Type string

const (
	TypeRunStarted     Type = "run.started"
	TypeUserMessage    Type = "user.message"
	TypeRunStep        Type = "run.step"
	TypeModelResponse  Type = "model.response"
	TypeTokenDelta     Type = "token.delta"
	TypeToolStart      Type = "tool.start"
	TypeToolResult     Type = "tool.result"
	TypeSteerMessage   Type = "steer.message"
	TypeRunPaused      Type = "run.paused"
	TypeRunResumed     Type = "run.resumed"
	TypeRunCompleted   Type = "run.completed"
	TypeRunError       Type = "run.error"
	TypeAgentSpawned   Type = "agent.spawned"
	TypeAgentCompleted Type = "agent.completed"
	TypeAgentHandoff   Type = "agent.handoff"
)

// Requirement 是 HITL 暂停时待用户决议的工具审批项(UI 协议)。
type Requirement struct {
	ID       string       `json:"id"`
	ToolCall llm.ToolCall `json:"tool_call"`
	Source   string       `json:"source,omitempty"`
}

// Event 是一条 append-only checkpoint / UI 事件。
type Event struct {
	Schema      string    `json:"schema"`
	Type        Type      `json:"type"`
	Timestamp   time.Time `json:"timestamp"`
	RunID       string    `json:"run_id"`
	ParentRunID string    `json:"parent_run_id,omitempty"`
	AgentName   string    `json:"agent_name,omitempty"`
	Depth       int       `json:"depth,omitempty"`
	Step        int       `json:"step,omitempty"`

	Response     *llm.Response `json:"response,omitempty"`
	ToolCall     *llm.ToolCall `json:"tool_call,omitempty"`
	Result       string        `json:"result,omitempty"`
	Delta        string        `json:"delta,omitempty"`
	Requirements []Requirement `json:"requirements,omitempty"`
	Error        string        `json:"error,omitempty"`
	ChildRunID   string        `json:"child_run_id,omitempty"`
	Source       string        `json:"source,omitempty"`
	Message      *llm.Message  `json:"message,omitempty"`
	Status       string        `json:"status,omitempty"`
}

// NewEvent 构造带 schema 与时间戳的事件。
func NewEvent(typ Type, runID string) Event {
	return Event{
		Schema:    SchemaVersion,
		Type:      typ,
		Timestamp: time.Now().UTC(),
		RunID:     runID,
	}
}

// WithFrame 填充编排帧(parent/depth/agent)。
func (e Event) WithFrame(parentRunID, agentName string, depth int) Event {
	e.ParentRunID = parentRunID
	e.AgentName = agentName
	e.Depth = depth
	return e
}

// WithStep 设置 step。
func (e Event) WithStep(step int) Event {
	e.Step = step
	return e
}
