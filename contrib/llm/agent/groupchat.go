package agent

import (
	"context"
	"fmt"
	"iter"
	"strings"

	"github.com/rushteam/beauty/contrib/llm"
)

// ==== 多 Agent 群聊:GroupChat ====

const defaultGroupChatMaxIterations = 10

// GroupChatManager 控制 group chat 的发言轮转、终止条件和历史广播。
// 所有回调可选(nil = 合理默认)。
type GroupChatManager struct {
	// SelectNextAgent 选择下一位发言者。返回 nil 表示会话结束。
	// history 包含完整对话历史。
	SelectNextAgent func(ctx context.Context, history []llm.Message, agents []Agent) (Agent, error)

	// ShouldTerminate 在每轮之前检查是否应结束会话。
	// iterationCount 从 0 开始递增。
	ShouldTerminate func(ctx context.Context, history []llm.Message, iterationCount int) (bool, error)

	// UpdateHistory 在将消息广播给其他参与者前过滤/转换历史。
	// nil 时默认透传所有消息。
	UpdateHistory func(ctx context.Context, history []llm.Message) ([]llm.Message, error)
}

// GroupChat 编排多个 Agent 的群聊对话。
// 每轮选一个 Agent 发言,发言结果广播给所有参与者(作为历史上下文)。
// 实现 Agent 接口,可被嵌套到 Chain/Parallel 等编排中。
type GroupChat struct {
	Name    string
	Agents  []Agent
	Manager GroupChatManager
	System  string // optional global system prompt

	// MaxIterations 最大轮数(默认 10)。
	MaxIterations int
}

var _ Agent = (*GroupChat)(nil)

func (gc *GroupChat) maxIterations() int {
	if gc.MaxIterations <= 0 {
		return defaultGroupChatMaxIterations
	}
	return gc.MaxIterations
}

func (gc *GroupChat) selectNext(ctx context.Context, history []llm.Message, agents []Agent) (Agent, error) {
	if gc.Manager.SelectNextAgent != nil {
		return gc.Manager.SelectNextAgent(ctx, history, agents)
	}
	return RoundRobinSelector()(ctx, history, agents)
}

func (gc *GroupChat) shouldTerminate(ctx context.Context, history []llm.Message, iteration int) (bool, error) {
	if gc.Manager.ShouldTerminate != nil {
		return gc.Manager.ShouldTerminate(ctx, history, iteration)
	}
	return false, nil
}

func (gc *GroupChat) broadcastHistory(ctx context.Context, history []llm.Message) ([]llm.Message, error) {
	if gc.Manager.UpdateHistory == nil {
		return history, nil
	}
	return gc.Manager.UpdateHistory(ctx, history)
}

func (gc *GroupChat) buildAgentRequest(req llm.Request, history []llm.Message) llm.Request {
	r := req
	r.Messages = history
	if gc.System != "" {
		if r.System != "" {
			r.System = gc.System + "\n\n" + r.System
		} else {
			r.System = gc.System
		}
	}
	return r
}

func (gc *GroupChat) appendTurn(history []llm.Message, name string, resp *llm.Response) []llm.Message {
	content := respContent(resp)
	if name != "" && content != "" {
		content = fmt.Sprintf("[%s]: %s", name, content)
	}
	return append(history, llm.Message{Role: llm.Assistant, Content: content})
}

func (gc *GroupChat) finalResponse(terminate bool, last *llm.Response, all []*llm.Response) *llm.Response {
	if terminate || len(all) <= 1 {
		if last != nil {
			return last
		}
		return &llm.Response{}
	}
	parts := make([]string, 0, len(all))
	var usage llm.Usage
	model := ""
	for _, r := range all {
		if r == nil {
			continue
		}
		if r.Content != "" {
			parts = append(parts, r.Content)
		}
		usage.InputTokens += r.Usage.InputTokens
		usage.OutputTokens += r.Usage.OutputTokens
		if model == "" {
			model = r.Model
		}
	}
	return &llm.Response{Content: strings.Join(parts, "\n\n"), Usage: usage, Model: model}
}

// Run 启动群聊,按 Manager 策略轮转发言,返回事件流。
func (gc *GroupChat) Run(ctx context.Context, req llm.Request, opts ...Option) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		if len(gc.Agents) == 0 {
			err := fmt.Errorf("agent: GroupChat has no agents")
			yield(Event{Type: EventError, AgentName: gc.Name, Err: err}, err)
			return
		}

		runID := newRunID()
		history := cloneMessages(req.Messages)
		maxIter := gc.maxIterations()
		var last *llm.Response
		var turnResponses []*llm.Response

		for iteration := 0; iteration < maxIter; iteration++ {
			if err := ctx.Err(); err != nil {
				yield(Event{Type: EventError, AgentName: gc.Name, RunID: runID, Err: err}, err)
				return
			}

			terminate, err := gc.shouldTerminate(ctx, history, iteration)
			if err != nil {
				yield(Event{Type: EventError, AgentName: gc.Name, RunID: runID, Err: err}, err)
				return
			}
			if terminate {
				final := gc.finalResponse(true, last, turnResponses)
				yield(Event{Type: EventFinal, AgentName: gc.Name, Response: final, RunID: runID}, nil)
				return
			}

			next, err := gc.selectNext(ctx, history, gc.Agents)
			if err != nil {
				yield(Event{Type: EventError, AgentName: gc.Name, RunID: runID, Err: err}, err)
				return
			}
			if next == nil {
				final := gc.finalResponse(true, last, turnResponses)
				yield(Event{Type: EventFinal, AgentName: gc.Name, Response: final, RunID: runID}, nil)
				return
			}

			broadcast, err := gc.broadcastHistory(ctx, history)
			if err != nil {
				yield(Event{Type: EventError, AgentName: gc.Name, RunID: runID, Err: err}, err)
				return
			}

			agentName := memberDisplayName(next, "")
			agentReq := gc.buildAgentRequest(req, broadcast)
			out := collectMemberRun(ctx, next, agentReq, nil, opts...)
			switch out.Status {
			case StatusDone:
				last = out.Response
				turnResponses = append(turnResponses, out.Response)
				history = gc.appendTurn(history, agentName, out.Response)
				if !yield(Event{
					Type:      EventStep,
					Step:      iteration + 1,
					Response:  out.Response,
					RunID:     runID,
					AgentName: agentName,
				}, nil) {
					return
				}
			case StatusPaused:
				yield(Event{
					Type:         EventPaused,
					AgentName:    gc.Name,
					Response:     out.Response,
					RunID:        runID,
					Requirements: out.Requirements,
				}, nil)
				return
			case StatusError:
				yield(Event{
					Type:      EventError,
					AgentName: gc.Name,
					Response:  out.Response,
					RunID:     runID,
					Err:       out.Err,
				}, out.Err)
				return
			default:
				err := fmt.Errorf("agent: GroupChat unexpected child status %q", out.Status)
				yield(Event{Type: EventError, AgentName: gc.Name, RunID: runID, Err: err}, err)
				return
			}
		}

		final := gc.finalResponse(false, last, turnResponses)
		yield(Event{Type: EventFinal, AgentName: gc.Name, Response: final, RunID: runID}, nil)
	}
}

// Continue 不支持 GroupChat(无暂停/恢复语义)。
func (gc *GroupChat) Continue(ctx context.Context, runID string, resolutions []Resolution, opts ...Option) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		err := fmt.Errorf("agent: GroupChat.Continue not supported")
		_ = resolutions
		_ = opts
		yield(Event{Type: EventError, AgentName: gc.Name, RunID: runID, Err: err}, err)
	}
}

// Info 实现 Agent。
func (gc *GroupChat) Info() Info {
	var tools []llm.ToolDef
	for _, a := range gc.Agents {
		if a != nil {
			tools = append(tools, a.Info().Tools...)
		}
	}
	return Info{Name: gc.Name, Description: "multi-agent group chat", Tools: tools}
}

// RoundRobinSelector 按顺序轮转发言者。
func RoundRobinSelector() func(ctx context.Context, history []llm.Message, agents []Agent) (Agent, error) {
	return func(_ context.Context, history []llm.Message, agents []Agent) (Agent, error) {
		valid := nonNilAgents(agents)
		if len(valid) == 0 {
			return nil, fmt.Errorf("agent: RoundRobinSelector: no agents")
		}
		turns := 0
		for _, m := range history {
			if m.Role == llm.Assistant {
				turns++
			}
		}
		return valid[turns%len(valid)], nil
	}
}

// ContentTerminator 检查最后一条消息是否包含终止关键词。
func ContentTerminator(keywords ...string) func(ctx context.Context, history []llm.Message, iterationCount int) (bool, error) {
	return func(_ context.Context, history []llm.Message, _ int) (bool, error) {
		if len(history) == 0 {
			return false, nil
		}
		content := history[len(history)-1].Content
		for _, kw := range keywords {
			if kw != "" && strings.Contains(content, kw) {
				return true, nil
			}
		}
		return false, nil
	}
}

// MaxIterationTerminator 在达到指定轮数后终止。
func MaxIterationTerminator(max int) func(ctx context.Context, history []llm.Message, iterationCount int) (bool, error) {
	return func(_ context.Context, _ []llm.Message, iterationCount int) (bool, error) {
		return iterationCount >= max, nil
	}
}

func nonNilAgents(agents []Agent) []Agent {
	valid := make([]Agent, 0, len(agents))
	for _, a := range agents {
		if a != nil {
			valid = append(valid, a)
		}
	}
	return valid
}
