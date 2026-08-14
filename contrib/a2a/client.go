package a2a

import (
	"context"
	"fmt"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

// ClientConfig 是 A2A 客户端 Agent 的配置。
type ClientConfig struct {
	// Name 是 agent 名称(用于 Info)。
	Name string
	// Description 是 agent 描述。
	Description string
}

// remoteAgent 将远程 A2A 服务包装为 beauty agent.Agent。
type remoteAgent struct {
	client *a2aclient.Client
	cfg    ClientConfig
	taskID a2a.TaskID
}

// NewAgent 将一个 A2A 客户端包装为 beauty agent.Agent。
// 调用方先通过 a2a-go SDK 建立连接(可从 AgentCard 解析):
//
//	card, _ := agentcard.DefaultResolver.Resolve(ctx, "http://remote:5000")
//	c, _ := a2aclient.NewFromCard(ctx, card)
//	a := a2a.NewAgent(c, a2a.ClientConfig{Name: "remote-agent"})
//	outcome := a.Run(ctx, llm.Request{Messages: msgs})
func NewAgent(c *a2aclient.Client, cfg ClientConfig) agent.Agent {
	return &remoteAgent{client: c, cfg: cfg}
}

func (a *remoteAgent) Info() agent.Info {
	return agent.Info{Name: a.cfg.Name, Description: a.cfg.Description}
}

func (a *remoteAgent) Run(ctx context.Context, req llm.Request) agent.RunOutcome {
	msg := a.buildMessage(req)

	result, err := a.client.SendMessage(ctx, &a2a.SendMessageRequest{
		Message: msg,
	})
	if err != nil {
		return agent.RunOutcome{Status: agent.StatusError, Err: fmt.Errorf("a2a: send message: %w", err)}
	}

	return a.handleResult(result)
}

func (a *remoteAgent) Continue(ctx context.Context, runID string, resolutions []agent.Resolution) agent.RunOutcome {
	var parts []*a2a.Part
	for _, r := range resolutions {
		if r.Approved {
			parts = append(parts, a2a.NewTextPart(fmt.Sprintf("[approved] %s", r.ID)))
		} else {
			parts = append(parts, a2a.NewTextPart(fmt.Sprintf("[denied] %s: %s", r.ID, r.Reason)))
		}
	}

	msg := a2a.NewMessage(a2a.MessageRoleUser, parts...)
	if a.taskID != "" {
		msg.TaskID = a.taskID
	}

	result, err := a.client.SendMessage(ctx, &a2a.SendMessageRequest{
		Message: msg,
	})
	if err != nil {
		return agent.RunOutcome{Status: agent.StatusError, Err: fmt.Errorf("a2a: continue: %w", err)}
	}
	return a.handleResult(result)
}

func (a *remoteAgent) buildMessage(req llm.Request) *a2a.Message {
	parts := messagesToParts(req.Messages)
	if req.System != "" {
		parts = append([]*a2a.Part{a2a.NewTextPart("[system] " + req.System)}, parts...)
	}

	msg := a2a.NewMessage(a2a.MessageRoleUser, parts...)
	if a.taskID != "" {
		msg.TaskID = a.taskID
	}
	return msg
}

func (a *remoteAgent) handleResult(result a2a.SendMessageResult) agent.RunOutcome {
	if result == nil {
		return agent.RunOutcome{Status: agent.StatusError, Err: fmt.Errorf("a2a: nil response")}
	}

	switch v := result.(type) {
	case *a2a.Task:
		a.taskID = v.ID
		return a.taskToOutcome(v)
	case *a2a.Message:
		msg := partsToMessage(v.Parts, a2aRoleToBeauty(v.Role))
		return agent.RunOutcome{
			Status:   agent.StatusDone,
			Messages: []llm.Message{msg},
			Response: &llm.Response{Content: msg.Content},
		}
	default:
		return agent.RunOutcome{Status: agent.StatusError, Err: fmt.Errorf("a2a: unexpected result type %T", result)}
	}
}

func (a *remoteAgent) taskToOutcome(task *a2a.Task) agent.RunOutcome {
	var messages []llm.Message
	for _, hist := range task.History {
		msg := partsToMessage(hist.Parts, a2aRoleToBeauty(hist.Role))
		messages = append(messages, msg)
	}

	for _, art := range task.Artifacts {
		msg := partsToMessage(art.Parts, llm.Assistant)
		messages = append(messages, msg)
	}

	state := task.Status.State
	switch state {
	case a2a.TaskStateCompleted:
		var content string
		if len(messages) > 0 {
			content = messages[len(messages)-1].Content
		}
		return agent.RunOutcome{
			Status:   agent.StatusDone,
			RunID:    string(task.ID),
			Messages: messages,
			Response: &llm.Response{Content: content},
		}
	case a2a.TaskStateInputRequired:
		return agent.RunOutcome{
			Status:   agent.StatusPaused,
			RunID:    string(task.ID),
			Messages: messages,
			Requirements: []agent.Requirement{{
				ID:       "a2a_input_required",
				ToolCall: llm.ToolCall{Name: "a2a_input", ID: string(task.ID)},
				Source:   "a2a:remote",
			}},
		}
	case a2a.TaskStateFailed:
		var errMsg string
		if task.Status.Message != nil {
			errMsg = textFromParts(task.Status.Message.Parts)
		}
		return agent.RunOutcome{
			Status: agent.StatusError,
			RunID:  string(task.ID),
			Err:    fmt.Errorf("a2a task failed: %s", errMsg),
		}
	case a2a.TaskStateCanceled:
		return agent.RunOutcome{Status: agent.StatusCancelled, RunID: string(task.ID), Messages: messages}
	default:
		var content string
		if len(messages) > 0 {
			content = messages[len(messages)-1].Content
		}
		return agent.RunOutcome{
			Status:   agent.StatusDone,
			RunID:    string(task.ID),
			Messages: messages,
			Response: &llm.Response{Content: content},
		}
	}
}
