package agui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
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

// remoteAgent 将远程 AG-UI 服务包装为 beauty agent.Agent。
type remoteAgent struct {
	endpoint string
	cfg      ClientConfig
	threadID string
}

// NewAgent 将远程 AG-UI 端点包装为 beauty agent.Agent。
//
// 使用方式:
//
//	a := agui.NewAgent("http://remote:8080/agent", agui.ClientConfig{Name: "remote"})
//	for ev, err := range a.Run(ctx, llm.Request{Messages: msgs}) { ... }
func NewAgent(endpoint string, cfg ClientConfig) agent.Agent {
	return &remoteAgent{endpoint: endpoint, cfg: cfg}
}

func (a *remoteAgent) Info() agent.Info {
	return agent.Info{Name: a.cfg.Name, Description: a.cfg.Description}
}

func (a *remoteAgent) Run(ctx context.Context, req llm.Request, _ ...agent.Option) iter.Seq2[agent.Event, error] {
	return func(yield func(agent.Event, error) bool) {
		if err := a.doStream(ctx, req, yield); err != nil {
			yield(agent.Event{Type: agent.EventError, Err: err}, nil)
		}
	}
}

func (a *remoteAgent) Continue(ctx context.Context, _ string, resolutions []agent.Resolution, _ ...agent.Option) iter.Seq2[agent.Event, error] {
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

func (a *remoteAgent) doStream(ctx context.Context, req llm.Request, yield func(agent.Event, error) bool) error {
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

	return a.parseSSE(resp.Body, yield)
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

func (a *remoteAgent) parseSSE(r io.Reader, yield func(agent.Event, error) bool) error {
	scanner := bufio.NewScanner(r)
	toolCalls := make(map[string]*llm.ToolCall)

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
			if !yield(agent.Event{
				Type:     agent.EventToken,
				Response: &llm.Response{Content: ev.Delta},
			}, nil) {
				return nil
			}

		case EventReasoningMessageContent:
			if !yield(agent.Event{
				Type:     agent.EventToken,
				Response: &llm.Response{Thinking: ev.Delta},
			}, nil) {
				return nil
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
				if !yield(agent.Event{
					Type:     agent.EventToolStart,
					ToolCall: tc,
				}, nil) {
					return nil
				}
				delete(toolCalls, ev.ToolCallID)
			}

		case EventToolCallResult:
			if !yield(agent.Event{
				Type:   agent.EventToolResult,
				Result: ev.Content,
				ToolCall: &llm.ToolCall{
					ID: ev.ToolCallID,
				},
			}, nil) {
				return nil
			}

		case EventRunFinished:
			content := allContent.String()
			if ev.Result != "" {
				content = ev.Result
			}
			yield(agent.Event{
				Type:     agent.EventFinal,
				Response: &llm.Response{Content: content},
			}, nil)
			return nil

		case EventRunError:
			yield(agent.Event{
				Type: agent.EventError,
				Err:  fmt.Errorf("agui remote error: %s", ev.Message),
			}, nil)
			return nil

		case EventStepStarted:
			if !yield(agent.Event{Type: agent.EventStep}, nil) {
				return nil
			}

		case EventStepFinished:
			// 忽略
		}
	}

	return scanner.Err()
}
