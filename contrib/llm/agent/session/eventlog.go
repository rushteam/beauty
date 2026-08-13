package session

import (
	"context"
	"fmt"
	"time"

	"github.com/rushteam/beauty/contrib/llm"
)

// SessionEventKind 标识会话事件类型。
type SessionEventKind string

const (
	EventUserMessage      SessionEventKind = "user_message"
	EventAssistantMessage SessionEventKind = "assistant_message"
	EventToolCall         SessionEventKind = "tool_call"
	EventToolResult       SessionEventKind = "tool_result"
	EventSystemMessage    SessionEventKind = "system_message"
	EventSteerMessage     SessionEventKind = "steer"
	EventInjectContext    SessionEventKind = "inject"
	EventSummaryGenerated SessionEventKind = "summary"
	EventSessionForked    SessionEventKind = "fork"
)

// SessionEvent 是会话中的一条不可变事件。
// 所有模型可见的内容都必须能从事件序列重建——这是核心不变量。
type SessionEvent struct {
	Kind      SessionEventKind `json:"kind"`
	Timestamp time.Time        `json:"timestamp"`
	Step      int              `json:"step,omitempty"`
	RunID     string           `json:"run_id,omitempty"`

	Message  *llm.Message  `json:"message,omitempty"`
	ToolCall *llm.ToolCall `json:"tool_call,omitempty"`
	Result   string        `json:"result,omitempty"`

	ForkFrom string `json:"fork_from,omitempty"`
	ForkAt   int    `json:"fork_at,omitempty"`
}

// Replay 从事件序列重建会话状态(摘要 + 消息列表)。
// 这是 event-sourcing 的核心投影函数。
func Replay(events []SessionEvent) (summary string, messages []llm.Message) {
	for _, ev := range events {
		switch ev.Kind {
		case EventUserMessage, EventAssistantMessage, EventSystemMessage, EventSteerMessage:
			if ev.Message != nil {
				messages = append(messages, *ev.Message)
			}
		case EventToolCall:
			if ev.Message != nil {
				messages = append(messages, *ev.Message)
			}
		case EventToolResult:
			if ev.ToolCall != nil {
				messages = append(messages, llm.Message{
					Role:       llm.Tool,
					ToolCallID: ev.ToolCall.ID,
					Content:    ev.Result,
				})
			}
		case EventSummaryGenerated:
			summary = ev.Result
			messages = nil
		}
	}
	return summary, messages
}

// Fork 从 sourceID 的事件日志中截取前 upToIndex 条事件(含),复制到 newID。
func Fork(ctx context.Context, store EventStore, sourceID, newID string, upToIndex int) error {
	events, err := store.LoadEvents(ctx, sourceID)
	if err != nil {
		return fmt.Errorf("session: fork: read source %q: %w", sourceID, err)
	}
	if events == nil {
		return fmt.Errorf("session: fork: source session %q not found", sourceID)
	}
	if upToIndex < 0 {
		upToIndex = 0
	}
	if upToIndex >= len(events) {
		upToIndex = len(events) - 1
	}

	copied := make([]SessionEvent, upToIndex+1, upToIndex+2)
	copy(copied, events[:upToIndex+1])

	copied = append(copied, SessionEvent{
		Kind:      EventSessionForked,
		Timestamp: time.Now(),
		ForkFrom:  sourceID,
		ForkAt:    upToIndex,
	})

	return store.AppendEvents(ctx, newID, copied...)
}

// ReplayToSession 从事件日志重建 Session 对象,兼容 Manager。
func ReplayToSession(id string, events []SessionEvent) *Session {
	summary, messages := Replay(events)
	return &Session{
		ID:        id,
		Summary:   summary,
		Messages:  messages,
		UpdatedAt: lastTimestamp(events),
	}
}

func lastTimestamp(events []SessionEvent) time.Time {
	if len(events) == 0 {
		return time.Time{}
	}
	return events[len(events)-1].Timestamp
}

// RecordMessages 把一组 llm.Message 转成 SessionEvent 并追加到事件日志。
func RecordMessages(ctx context.Context, store EventStore, sessionID string, step int, runID string, msgs []llm.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	events := make([]SessionEvent, 0, len(msgs))
	now := time.Now()
	for _, m := range msgs {
		ev := SessionEvent{
			Timestamp: now,
			Step:      step,
			RunID:     runID,
			Message:   &m,
		}
		switch m.Role {
		case llm.User:
			ev.Kind = EventUserMessage
		case llm.Assistant:
			if len(m.ToolCalls) > 0 {
				ev.Kind = EventToolCall
			} else {
				ev.Kind = EventAssistantMessage
			}
		case llm.Tool:
			ev.Kind = EventToolResult
			ev.Result = m.Content
		case llm.System:
			ev.Kind = EventSystemMessage
		default:
			ev.Kind = EventUserMessage
		}
		events = append(events, ev)
	}
	return store.AppendEvents(ctx, sessionID, events...)
}
