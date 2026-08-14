// Package httpui 把 StreamAgent 运行事件转为 SSE(checkpoint beauty.agent.v1 schema),供 HITL 前端消费。
package httpui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
	"github.com/rushteam/beauty/contrib/llm/agent/checkpoint"
)

// Handler 把 agent 流式事件以 SSE 推送给客户端。
type Handler struct {
	Agent agent.StreamAgent
	Name  string
	Store agent.RunStore // 可选;非 nil 时同时写入 CheckpointStore 事件日志
}

func (h *Handler) agentName() string {
	if h.Name != "" {
		return h.Name
	}
	return h.Agent.Info().Name
}

// RunRequest 是 POST /run 的请求体。
type RunRequest struct {
	Model    string        `json:"model"`
	System   string        `json:"system,omitempty"`
	Messages []llm.Message `json:"messages"`
}

// ContinueRequest 是 POST /continue 的请求体。
type ContinueRequest struct {
	RunID       string             `json:"run_id"`
	Resolutions []agent.Resolution `json:"resolutions"`
}

// ServeHTTP 路由:
//   - POST /run       → RunStream SSE
//   - POST /continue  → ContinueStream SSE
//   - GET  /events?run_id= → 回放已持久化 checkpoint 事件(SSE)
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/run":
		h.serveRun(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/continue":
		h.serveContinue(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/events":
		h.serveReplay(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) serveRun(w http.ResponseWriter, r *http.Request) {
	var body RunRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req := llm.Request{Model: body.Model, System: body.System, Messages: body.Messages}
	if err := StreamAgentRun(r.Context(), w, h.Agent, h.agentName(), h.Store, req); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) serveContinue(w http.ResponseWriter, r *http.Request) {
	var body ContinueRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if body.RunID == "" {
		http.Error(w, "run_id required", http.StatusBadRequest)
		return
	}
	if err := StreamAgentContinue(r.Context(), w, h.Agent, h.agentName(), h.Store, body.RunID, body.Resolutions); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) serveReplay(w http.ResponseWriter, r *http.Request) {
	runID := r.URL.Query().Get("run_id")
	if runID == "" {
		http.Error(w, "run_id query required", http.StatusBadRequest)
		return
	}
	events, err := agent.LoadUIEventsFromStore(r.Context(), h.Store, runID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := WriteEventsSSE(w, events); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// StreamAgentRun 执行 RunStream 并把事件写成 SSE。
func StreamAgentRun(ctx context.Context, w http.ResponseWriter, sa agent.StreamAgent, name string, store agent.RunStore, req llm.Request) error {
	fl, err := prepareSSE(w)
	if err != nil {
		return err
	}
	frame := checkpoint.Frame{AgentName: name}
	for ev := range sa.RunStream(ctx, req) {
		if err := writeAgentEvent(ctx, w, fl, store, frame, ev); err != nil {
			return err
		}
	}
	return nil
}

// StreamAgentContinue 执行 ContinueStream 并把事件写成 SSE。
func StreamAgentContinue(ctx context.Context, w http.ResponseWriter, sa agent.StreamAgent, name string, store agent.RunStore, runID string, resolutions []agent.Resolution) error {
	fl, err := prepareSSE(w)
	if err != nil {
		return err
	}
	frame := checkpoint.Frame{AgentName: name}
	for ev := range sa.ContinueStream(ctx, runID, resolutions) {
		if err := writeAgentEvent(ctx, w, fl, store, frame, ev); err != nil {
			return err
		}
	}
	return nil
}

// WriteEventsSSE 把 checkpoint 事件序列写成 SSE(回放/审计)。
func WriteEventsSSE(w http.ResponseWriter, events []checkpoint.Event) error {
	fl, err := prepareSSE(w)
	if err != nil {
		return err
	}
	for _, ev := range events {
		if err := checkpoint.WriteSSE(w, ev); err != nil {
			return err
		}
		fl.Flush()
	}
	return nil
}

func prepareSSE(w http.ResponseWriter) (http.Flusher, error) {
	fl, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("httpui: ResponseWriter does not support Flush")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	return fl, nil
}

func writeAgentEvent(ctx context.Context, w io.Writer, fl http.Flusher, store agent.RunStore, frame checkpoint.Frame, ev agent.Event) error {
	if ev.AgentName != "" {
		frame.AgentName = ev.AgentName
	}
	if ev.RunID != "" {
		frame.RunID = ev.RunID
	}
	ce := agent.AgentEventToCheckpoint(ev, frame)
	if store != nil && ce.RunID != "" {
		if log, ok := store.(checkpoint.EventLog); ok {
			_ = log.AppendEvents(ctx, ce.RunID, ce)
		}
	}
	if err := checkpoint.WriteSSE(w, ce); err != nil {
		return err
	}
	fl.Flush()
	return nil
}
