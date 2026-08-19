package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

// ──────────────────────────────────────────────────────────────────────────────
// Server: 将 beauty agent 暴露为 A2A 服务
// ──────────────────────────────────────────────────────────────────────────────

// ServerConfig 是 A2A 服务端配置。
type ServerConfig struct {
	// AgentCard 是对外发布的 Agent 卡片(名称、能力、技能等)。
	AgentCard *a2a.AgentCard
}

// executor 实现 a2asrv.AgentExecutor 接口。
type executor struct {
	agent agent.Agent
	cfg   ServerConfig
}

// NewExecutor 把 beauty Agent 桥接为 A2A a2asrv.AgentExecutor。
// 配合 a2a-go 的 server 包启动 HTTP 服务:
//
//	exec := a2a.NewExecutor(myAgent, a2a.ServerConfig{AgentCard: card})
//	handler := a2asrv.NewHandler(exec)
//	mux.Handle("/", a2asrv.NewJSONRPCHandler(handler))
func NewExecutor(a agent.Agent, cfg ServerConfig) a2asrv.AgentExecutor {
	return &executor{agent: a, cfg: cfg}
}

// Execute 处理来自 A2A 客户端的请求,将其转为 beauty agent 运行,
// 并把结果流式输出为 A2A 事件序列。
func (e *executor) Execute(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		msg := execCtx.Message
		if msg == nil {
			yield(nil, fmt.Errorf("a2a server: nil message"))
			return
		}

		beautyMsg := partsToMessage(msg.Parts, a2aRoleToBeauty(msg.Role))
		req := llm.Request{Messages: []llm.Message{beautyMsg}}

		if execCtx.StoredTask != nil {
			for _, hist := range execCtx.StoredTask.History {
				m := partsToMessage(hist.Parts, a2aRoleToBeauty(hist.Role))
				req.Messages = append([]llm.Message{m}, req.Messages...)
			}
		}

		// 发出 Submitted + Working 状态
		if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateSubmitted, nil), nil) {
			return
		}
		if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateWorking, nil), nil) {
			return
		}

		var allText strings.Builder
		var artifactID a2a.ArtifactID

		for ev, err := range e.agent.Run(ctx, req) {
			if err != nil {
				yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed,
					a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(err.Error())),
				), nil)
				return
			}
			switch ev.Type {
			case agent.EventToken:
				if ev.Response != nil && ev.Response.Content != "" {
					allText.WriteString(ev.Response.Content)
					var event *a2a.TaskArtifactUpdateEvent
					if artifactID == "" {
						event = a2a.NewArtifactEvent(execCtx, a2a.NewTextPart(ev.Response.Content))
						artifactID = event.Artifact.ID
					} else {
						event = a2a.NewArtifactUpdateEvent(execCtx, artifactID, a2a.NewTextPart(ev.Response.Content))
						event.Append = true
					}
					if !yield(event, nil) {
						return
					}
				}

			case agent.EventFinal:
				yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCompleted, nil), nil)
				return

			case agent.EventPaused:
				yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateInputRequired,
					a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("awaiting approval")),
				), nil)
				return

			case agent.EventToolStart:
				if ev.ToolCall != nil {
					info, _ := json.Marshal(map[string]any{
						"tool": ev.ToolCall.Name,
						"args": ev.ToolCall.Arguments,
					})
					event := a2a.NewArtifactEvent(execCtx, a2a.NewDataPart(json.RawMessage(info)))
					event.Artifact.Name = "tool_call:" + ev.ToolCall.Name
					if !yield(event, nil) {
						return
					}
				}

			case agent.EventError:
				errMsg := "unknown error"
				if ev.Err != nil {
					errMsg = ev.Err.Error()
				}
				yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed,
					a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(errMsg)),
				), nil)
				return
			}
		}

		// channel 关闭且无终态(防御性完成)
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCompleted, nil), nil)
	}
}

// Cancel 处理 A2A 任务取消请求。
func (e *executor) Cancel(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCanceled, nil), nil)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// 便捷路由注册
// ──────────────────────────────────────────────────────────────────────────────

// RegisterRoutes 在 mux 上注册完整的 A2A 端点(JSON-RPC + AgentCard well-known)。
//
//	mux := http.NewServeMux()
//	a2a.RegisterRoutes(mux, myStreamAgent, a2a.ServerConfig{AgentCard: card})
//	http.ListenAndServe(":5000", mux)
func RegisterRoutes(mux *http.ServeMux, a agent.Agent, cfg ServerConfig) {
	exec := NewExecutor(a, cfg)
	var opts []a2asrv.RequestHandlerOption
	if cfg.AgentCard != nil {
		opts = append(opts, a2asrv.WithExtendedAgentCard(cfg.AgentCard))
	}
	handler := a2asrv.NewHandler(exec, opts...)
	mux.Handle("/", a2asrv.NewJSONRPCHandler(handler))
	if cfg.AgentCard != nil {
		mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(cfg.AgentCard))
	}
}
