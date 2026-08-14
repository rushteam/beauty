package agui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

// ClientConfig 是 AG-UI 客户端配置。
type ClientConfig struct {
	// Name 是 agent 名称(用于 Info)。
	Name string
	// Description 是 agent 描述。
	Description string
	// HTTPClient 可选的 HTTP 客户端;为 nil 时使用 http.DefaultClient。
	HTTPClient *http.Client
}

// remoteAgent 将远程 AG-UI 服务包装为 beauty agent.Agent / StreamAgent。
type remoteAgent struct {
	endpoint string
	cfg      ClientConfig
	threadID string
}

// NewAgent 将远程 AG-UI 端点包装为 beauty agent.Agent 和 StreamAgent。
//
// 使用方式:
//
//	a := agui.NewAgent("http://remote:8080/agent", agui.ClientConfig{Name: "remote"})
//	outcome := a.Run(ctx, llm.Request{Messages: msgs})
//
// 或流式:
//
//	ch := a.(agent.StreamAgent).RunStream(ctx, req)
func NewAgent(endpoint string, cfg ClientConfig) agent.StreamAgent {
	return &remoteAgent{endpoint: endpoint, cfg: cfg}
}

func (a *remoteAgent) Info() agent.Info {
	return agent.Info{Name: a.cfg.Name, Description: a.cfg.Description}
}

func (a *remoteAgent) Run(ctx context.Context, req llm.Request) agent.RunOutcome {
	var messages []llm.Message
	var finalContent string

	for ev := range a.RunStream(ctx, req) {
		switch ev.Type {
		case agent.EventFinal:
			if ev.Response != nil {
				finalContent = ev.Response.Content
			}
		case agent.EventToken:
			if ev.Response != nil {
				finalContent += ev.Response.Content
			}
		case agent.EventError:
			return agent.RunOutcome{Status: agent.StatusError, Err: ev.Err}
		}
	}

	messages = append(messages, llm.Message{Role: llm.Assistant, Content: finalContent})
	return agent.RunOutcome{
		Status:   agent.StatusDone,
		Messages: messages,
		Response: &llm.Response{Content: finalContent},
	}
}

func (a *remoteAgent) Continue(ctx context.Context, runID string, resolutions []agent.Resolution) agent.RunOutcome {
	// AG-UI 不原生支持 continue,通过新消息模拟
	var parts []string
	for _, r := range resolutions {
		if r.Approved {
			parts = append(parts, fmt.Sprintf("[approved] %s", r.ID))
		} else {
			parts = append(parts, fmt.Sprintf("[denied] %s: %s", r.ID, r.Reason))
		}
	}
	req := llm.Request{Messages: []llm.Message{{Role: llm.User, Content: strings.Join(parts, "\n")}}}
	return a.Run(ctx, req)
}

func (a *remoteAgent) RunStream(ctx context.Context, req llm.Request) <-chan agent.Event {
	ch := make(chan agent.Event, 32)
	go func() {
		defer close(ch)
		if err := a.doStream(ctx, req, ch); err != nil {
			ch <- agent.Event{Type: agent.EventError, Err: err}
		}
	}()
	return ch
}

func (a *remoteAgent) ContinueStream(ctx context.Context, runID string, resolutions []agent.Resolution) <-chan agent.Event {
	var parts []string
	for _, r := range resolutions {
		if r.Approved {
			parts = append(parts, fmt.Sprintf("[approved] %s", r.ID))
		} else {
			parts = append(parts, fmt.Sprintf("[denied] %s: %s", r.ID, r.Reason))
		}
	}
	req := llm.Request{Messages: []llm.Message{{Role: llm.User, Content: strings.Join(parts, "\n")}}}
	return a.RunStream(ctx, req)
}

func (a *remoteAgent) doStream(ctx context.Context, req llm.Request, ch chan<- agent.Event) error {
	input := a.buildInput(req)
	body, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("agui client: marshal input: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("agui client: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	client := a.cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("agui client: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("agui client: status %d: %s", resp.StatusCode, string(b))
	}

	return a.parseSSE(resp.Body, ch)
}

func (a *remoteAgent) buildInput(req llm.Request) *RunAgentInput {
	input := &RunAgentInput{
		ThreadID: a.threadID,
	}
	for _, m := range req.Messages {
		im := InputMessage{
			Role:    beautyRoleToAGUI(m.Role),
			Content: m.Content,
		}
		if m.ToolCallID != "" {
			im.ToolCallID = m.ToolCallID
		}
		for _, tc := range m.ToolCalls {
			im.ToolCalls = append(im.ToolCalls, InputToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: InputFunction{
					Name:      tc.Name,
					Arguments: string(tc.Arguments),
				},
			})
		}
		input.Messages = append(input.Messages, im)
	}
	for _, t := range req.Tools {
		input.Tools = append(input.Tools, InputTool{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		})
	}
	return input
}

func (a *remoteAgent) parseSSE(r io.Reader, ch chan<- agent.Event) error {
	scanner := bufio.NewScanner(r)
	// 累积工具调用参数
	toolCalls := make(map[string]*llm.ToolCall) // toolCallId → accumulated

	var allContent strings.Builder

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "" {
			continue
		}

		var ev Event
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}

		switch ev.Type {
		case EventRunStarted:
			if ev.ThreadID != "" {
				a.threadID = ev.ThreadID
			}

		case EventTextMessageContent:
			allContent.WriteString(ev.Delta)
			ch <- agent.Event{
				Type:     agent.EventToken,
				Response: &llm.Response{Content: ev.Delta},
			}

		case EventReasoningMessageContent:
			ch <- agent.Event{
				Type:     agent.EventToken,
				Response: &llm.Response{Thinking: ev.Delta},
			}

		case EventToolCallStart:
			toolCalls[ev.ToolCallID] = &llm.ToolCall{
				ID:   ev.ToolCallID,
				Name: ev.ToolCallName,
			}

		case EventToolCallArgs:
			if tc, ok := toolCalls[ev.ToolCallID]; ok {
				tc.Arguments = append(tc.Arguments, []byte(ev.Delta)...)
			}

		case EventToolCallEnd:
			if tc, ok := toolCalls[ev.ToolCallID]; ok {
				ch <- agent.Event{
					Type:     agent.EventToolStart,
					ToolCall: tc,
				}
				delete(toolCalls, ev.ToolCallID)
			}

		case EventToolCallResult:
			ch <- agent.Event{
				Type:   agent.EventToolResult,
				Result: ev.Content,
				ToolCall: &llm.ToolCall{
					ID: ev.ToolCallID,
				},
			}

		case EventRunFinished:
			content := allContent.String()
			if ev.Result != "" {
				content = ev.Result
			}
			ch <- agent.Event{
				Type:     agent.EventFinal,
				Response: &llm.Response{Content: content},
			}
			return nil

		case EventRunError:
			ch <- agent.Event{
				Type: agent.EventError,
				Err:  fmt.Errorf("agui remote error: %s", ev.Message),
			}
			return nil

		case EventStepStarted:
			ch <- agent.Event{Type: agent.EventStep}

		case EventStepFinished:
			// 忽略
		}
	}

	return scanner.Err()
}
