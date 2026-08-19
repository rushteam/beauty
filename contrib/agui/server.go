package agui

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

// HandlerConfig 是 AG-UI 服务端配置。
type HandlerConfig struct {
	// AgentName 覆盖 agent.Info().Name。
	AgentName string
}

// Handler 将 beauty Agent 暴露为 AG-UI HTTP 端点。
// 请求方式: POST 发送 RunAgentInput JSON → 响应为 SSE 事件流。
//
// 使用方式:
//
//	h := agui.NewHandler(myAgent, agui.HandlerConfig{})
//	http.Handle("/agent", h)
type Handler struct {
	Agent agent.Agent
	Cfg   HandlerConfig
}

// NewHandler 创建 AG-UI HTTP 处理器。
func NewHandler(a agent.Agent, cfg HandlerConfig) *Handler {
	return &Handler{Agent: a, Cfg: cfg}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var input RunAgentInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}

	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	threadID := input.ThreadID
	if threadID == "" {
		threadID = generateID("thread")
	}
	runID := input.RunID
	if runID == "" {
		runID = generateID("run")
	}

	writeEvent(w, fl, Event{
		Type:      EventRunStarted,
		ThreadID:  threadID,
		RunID:     runID,
		Timestamp: nowTimestamp(),
	})

	messages := inputToBeautyMessages(&input)
	req := llm.Request{Messages: messages}
	if len(input.Tools) > 0 {
		req.Tools = inputToBeautyTools(input.Tools)
	}

	var currentMsgID string
	var inTextMessage bool
	var inReasoning bool

	for ev, err := range h.Agent.Run(r.Context(), req) {
		if err != nil {
			writeEvent(w, fl, Event{
				Type:      EventRunError,
				ThreadID:  threadID,
				RunID:     runID,
				Message:   err.Error(),
				Timestamp: nowTimestamp(),
			})
			return
		}
		switch ev.Type {
		case agent.EventToken:
			if ev.Response == nil {
				continue
			}
			// 思考内容
			if ev.Response.Thinking != "" {
				if !inReasoning {
					inReasoning = true
					writeEvent(w, fl, Event{
						Type:      EventReasoningMessageStart,
						ThreadID:  threadID,
						RunID:     runID,
						Timestamp: nowTimestamp(),
					})
				}
				writeEvent(w, fl, Event{
					Type:      EventReasoningMessageContent,
					ThreadID:  threadID,
					RunID:     runID,
					Delta:     ev.Response.Thinking,
					Timestamp: nowTimestamp(),
				})
				continue
			}
			// 文本内容
			if ev.Response.Content != "" {
				if inReasoning {
					inReasoning = false
					writeEvent(w, fl, Event{
						Type:      EventReasoningMessageEnd,
						ThreadID:  threadID,
						RunID:     runID,
						Timestamp: nowTimestamp(),
					})
				}
				if !inTextMessage {
					inTextMessage = true
					currentMsgID = generateID("msg")
					writeEvent(w, fl, Event{
						Type:      EventTextMessageStart,
						ThreadID:  threadID,
						RunID:     runID,
						MessageID: currentMsgID,
						Role:      "assistant",
						Timestamp: nowTimestamp(),
					})
				}
				writeEvent(w, fl, Event{
					Type:      EventTextMessageContent,
					ThreadID:  threadID,
					RunID:     runID,
					MessageID: currentMsgID,
					Delta:     ev.Response.Content,
					Timestamp: nowTimestamp(),
				})
			}

		case agent.EventStep:
			if inTextMessage {
				inTextMessage = false
				writeEvent(w, fl, Event{
					Type:      EventTextMessageEnd,
					ThreadID:  threadID,
					RunID:     runID,
					MessageID: currentMsgID,
					Timestamp: nowTimestamp(),
				})
			}
			writeEvent(w, fl, Event{
				Type:      EventStepStarted,
				ThreadID:  threadID,
				RunID:     runID,
				StepName:  fmt.Sprintf("step-%d", ev.Step),
				Timestamp: nowTimestamp(),
			})

		case agent.EventToolStart:
			if ev.ToolCall == nil {
				continue
			}
			writeEvent(w, fl, Event{
				Type:         EventToolCallStart,
				ThreadID:     threadID,
				RunID:        runID,
				ToolCallID:   ev.ToolCall.ID,
				ToolCallName: ev.ToolCall.Name,
				Timestamp:    nowTimestamp(),
			})
			if len(ev.ToolCall.Arguments) > 0 {
				writeEvent(w, fl, Event{
					Type:       EventToolCallArgs,
					ThreadID:   threadID,
					RunID:      runID,
					ToolCallID: ev.ToolCall.ID,
					Delta:      string(ev.ToolCall.Arguments),
					Timestamp:  nowTimestamp(),
				})
			}
			writeEvent(w, fl, Event{
				Type:       EventToolCallEnd,
				ThreadID:   threadID,
				RunID:      runID,
				ToolCallID: ev.ToolCall.ID,
				Timestamp:  nowTimestamp(),
			})

		case agent.EventToolResult:
			if ev.ToolCall != nil {
				writeEvent(w, fl, Event{
					Type:       EventToolCallResult,
					ThreadID:   threadID,
					RunID:      runID,
					MessageID:  fmt.Sprintf("result-%s", ev.ToolCall.ID),
					ToolCallID: ev.ToolCall.ID,
					Content:    ev.Result,
					Timestamp:  nowTimestamp(),
				})
			}

		case agent.EventFinal:
			if inTextMessage {
				writeEvent(w, fl, Event{
					Type:      EventTextMessageEnd,
					ThreadID:  threadID,
					RunID:     runID,
					MessageID: currentMsgID,
					Timestamp: nowTimestamp(),
				})
			}
			if inReasoning {
				writeEvent(w, fl, Event{
					Type:      EventReasoningMessageEnd,
					ThreadID:  threadID,
					RunID:     runID,
					Timestamp: nowTimestamp(),
				})
			}
			var result string
			if ev.Response != nil {
				result = ev.Response.Content
			}
			writeEvent(w, fl, Event{
				Type:      EventRunFinished,
				ThreadID:  threadID,
				RunID:     runID,
				Result:    result,
				Timestamp: nowTimestamp(),
			})
			return

		case agent.EventPaused:
			if inTextMessage {
				writeEvent(w, fl, Event{
					Type:      EventTextMessageEnd,
					ThreadID:  threadID,
					RunID:     runID,
					MessageID: currentMsgID,
					Timestamp: nowTimestamp(),
				})
			}
			writeEvent(w, fl, Event{
				Type:      EventRunFinished,
				ThreadID:  threadID,
				RunID:     runID,
				Timestamp: nowTimestamp(),
			})
			return

		case agent.EventError:
			errMsg := "unknown error"
			if ev.Err != nil {
				errMsg = ev.Err.Error()
			}
			writeEvent(w, fl, Event{
				Type:      EventRunError,
				ThreadID:  threadID,
				RunID:     runID,
				Message:   errMsg,
				Timestamp: nowTimestamp(),
			})
			return
		}
	}

	// channel 关闭但没有终态事件(理论上不应该发生,防御性结束)
	if inTextMessage {
		writeEvent(w, fl, Event{
			Type:      EventTextMessageEnd,
			ThreadID:  threadID,
			RunID:     runID,
			MessageID: currentMsgID,
			Timestamp: nowTimestamp(),
		})
	}
	writeEvent(w, fl, Event{
		Type:      EventRunFinished,
		ThreadID:  threadID,
		RunID:     runID,
		Timestamp: nowTimestamp(),
	})
}

func writeEvent(w http.ResponseWriter, fl http.Flusher, ev Event) {
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
	fl.Flush()
}
