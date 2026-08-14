package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent/checkpoint"
)

// ==== 多 Agent 薄编排:Agent-as-Tool / Chain ====

var agentToolParams = json.RawMessage(`{
  "type":"object",
  "properties":{
    "input":{"type":"string","description":"交给子 agent 的任务或问题"}
  },
  "required":["input"]
}`)

// AgentToolConfig 配置 AgentAsTool。
type AgentToolConfig struct {
	Model  string
	System string
}

// AgentToolOption 配置 AgentAsTool。
type AgentToolOption func(*AgentToolConfig)

// WithAgentToolModel 指定子 agent 使用的模型。
func WithAgentToolModel(model string) AgentToolOption {
	return func(c *AgentToolConfig) { c.Model = model }
}

// WithAgentToolSystem 给子 agent 追加/覆盖 system prompt。
func WithAgentToolSystem(system string) AgentToolOption {
	return func(c *AgentToolConfig) { c.System = system }
}

// AgentAsTool 把一个子 Agent 包装成可被父 agent 调用的 Tool。
// 子 Run 若 Paused,通过 NestedPauseError 冒泡,父 Runner 进入 Paused 并可 Continue。
func AgentAsTool(name, description string, sub Agent, opts ...AgentToolOption) Tool {
	cfg := &AgentToolConfig{}
	for _, o := range opts {
		o(cfg)
	}
	if description == "" {
		description = "委托子 agent 处理子任务,返回其最终文本答复"
	}
	source := "tool:" + name
	return Func(name, description, agentToolParams, func(ctx context.Context, args json.RawMessage) (string, error) {
		var in struct {
			Input string `json:"input"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", fmt.Errorf("agent tool: bad args: %w", err)
		}
		if in.Input == "" {
			return "", fmt.Errorf("agent tool: input required")
		}
		req := llm.Request{
			Model:    cfg.Model,
			System:   cfg.System,
			Messages: []llm.Message{{Role: llm.User, Content: in.Input}},
		}
		childCtx := WithTrigger(ctx, TriggerToolCall, name)
		if parent := checkpoint.FrameFrom(ctx); parent.RunID != "" {
			childCtx = checkpoint.WithFrame(childCtx, checkpoint.Frame{
				ParentRunID: parent.RunID,
				AgentName:   name,
				Depth:       parent.Depth + 1,
			})
		}
		out := sub.Run(childCtx, req)
		switch out.Status {
		case StatusDone:
			if out.Response != nil {
				return out.Response.Content, nil
			}
			return "", nil
		case StatusPaused:
			childID := out.RunID
			return "", &NestedPauseError{
				Child:  out,
				Source: source,
				Resume: func(ctx context.Context, resolutions []Resolution) RunOutcome {
					return sub.Continue(ctx, childID, resolutions)
				},
			}
		default:
			if out.Response != nil && out.Response.Content != "" && out.Err != nil {
				return out.Response.Content + "\n(error: " + out.Err.Error() + ")", nil
			}
			if out.Err != nil {
				return "", out.Err
			}
			return "", fmt.Errorf("agent tool: unexpected status %q", out.Status)
		}
	})
}

// ChainStep 是 Chain 中的一步。
type ChainStep struct {
	Name   string
	Agent  Agent
	Runner *Runner
	Model  string
	System string
}

func (s ChainStep) agent() Agent {
	if s.Agent != nil {
		return s.Agent
	}
	if s.Runner != nil {
		return s.Runner
	}
	return nil
}

type chainResume struct {
	agent   Agent
	childID string
}

// Chain 按序跑多个 agent。任一步 Paused 则整链 Paused;Continue 从该步恢复。
type Chain struct {
	Name  string
	Steps []ChainStep
	Store RunStore

	resumes sync.Map // runID → chainResume
}

var _ StreamAgent = (*Chain)(nil)

func (c *Chain) ensureStore() {
	if c.Store == nil {
		c.Store = NewMemoryRunStore()
	}
}

func (c *Chain) stepReq(cur llm.Request, i int, prevContent string) llm.Request {
	step := c.Steps[i]
	r := cur
	if step.Model != "" {
		r.Model = step.Model
	}
	if step.System != "" {
		r.System = step.System
	}
	if i > 0 {
		r.Messages = []llm.Message{{Role: llm.User, Content: prevContent}}
	}
	return r
}

func (c *Chain) stepSource(i int) string {
	if c.Steps[i].Name != "" {
		return "chain:" + c.Steps[i].Name
	}
	return fmt.Sprintf("chain:%d", i)
}

// Run 顺序执行各步。
func (c *Chain) Run(ctx context.Context, req llm.Request) RunOutcome {
	c.ensureStore()
	return c.runFrom(ctx, newRunID(), req, 0, "")
}

// Continue 从暂停步恢复。
func (c *Chain) Continue(ctx context.Context, runID string, resolutions []Resolution) RunOutcome {
	c.ensureStore()
	snap, err := c.Store.Load(ctx, runID)
	if err != nil {
		return outcomeError(runID, nil, nil, err)
	}
	if snap == nil || snap.Kind != "chain" {
		return outcomeError(runID, nil, nil, fmt.Errorf("agent: unknown chain runID %q", runID))
	}
	v, ok := c.resumes.Load(runID)
	if !ok {
		return outcomeError(runID, nil, nil, fmt.Errorf("agent: chain resume lost for %q", runID))
	}
	cr := v.(chainResume)
	out := cr.agent.Continue(ctx, cr.childID, resolutions)
	switch out.Status {
	case StatusPaused:
		reqs := remapRequirements(out.Requirements, snap.ChildSource)
		snap.Requirements = reqs
		snap.ChildRunID = out.RunID
		c.resumes.Store(runID, chainResume{agent: cr.agent, childID: out.RunID})
		if err := c.Store.Save(ctx, runID, snap); err != nil {
			return outcomeError(runID, out.Response, nil, err)
		}
		return outcomePaused(runID, out.Response, out.Messages, reqs)
	case StatusError:
		return outcomeError(runID, out.Response, out.Messages, out.Err)
	case StatusDone:
		c.resumes.Delete(runID)
		content := ""
		if out.Response != nil {
			content = out.Response.Content
		}
		_ = c.Store.Delete(ctx, runID)
		return c.runFrom(ctx, runID, snap.Request, snap.ChainStep+1, content)
	default:
		return outcomeError(runID, out.Response, out.Messages, fmt.Errorf("agent: chain unexpected status %q", out.Status))
	}
}

func (c *Chain) runFrom(ctx context.Context, runID string, req llm.Request, start int, prevContent string) RunOutcome {
	if len(c.Steps) == 0 {
		return outcomeError(runID, nil, nil, fmt.Errorf("agent: empty chain"))
	}
	var last *llm.Response
	content := prevContent
	for i := start; i < len(c.Steps); i++ {
		if err := ctx.Err(); err != nil {
			return outcomeError(runID, last, nil, err)
		}
		a := c.Steps[i].agent()
		if a == nil {
			return outcomeError(runID, last, nil, fmt.Errorf("agent: chain step %d (%s): nil agent", i, c.Steps[i].Name))
		}
		out := a.Run(ctx, c.stepReq(req, i, content))
		switch out.Status {
		case StatusPaused:
			src := c.stepSource(i)
			reqs := remapRequirements(out.Requirements, src)
			snap := &RunSnapshot{
				Kind:         "chain",
				Request:      req,
				ChainStep:    i,
				LastContent:  content,
				ChildRunID:   out.RunID,
				ChildSource:  src,
				Requirements: reqs,
			}
			if err := c.Store.Save(ctx, runID, snap); err != nil {
				return outcomeError(runID, out.Response, nil, err)
			}
			c.resumes.Store(runID, chainResume{agent: a, childID: out.RunID})
			return outcomePaused(runID, out.Response, out.Messages, reqs)
		case StatusError:
			return outcomeError(runID, out.Response, out.Messages, out.Err)
		case StatusDone:
			last = out.Response
			if last != nil {
				content = last.Content
			} else {
				content = ""
			}
		default:
			return outcomeError(runID, out.Response, out.Messages, fmt.Errorf("agent: chain unexpected status %q", out.Status))
		}
	}
	_ = c.Store.Delete(ctx, runID)
	return outcomeDone(runID, last, nil)
}

// RunStream 前 n-1 步同步,最后一步流式;Paused 时发 EventPaused。
func (c *Chain) RunStream(ctx context.Context, req llm.Request) <-chan Event {
	return streamAgentOutcome(c.Name, func(emit func(Event)) RunOutcome {
		return c.Run(ctx, req)
	})
}

// ContinueStream 是 Continue 的流式版。
func (c *Chain) ContinueStream(ctx context.Context, runID string, resolutions []Resolution) <-chan Event {
	return streamAgentOutcome(c.Name, func(emit func(Event)) RunOutcome {
		return c.Continue(ctx, runID, resolutions)
	})
}

func streamAgentOutcome(name string, fn func(emit func(Event)) RunOutcome) <-chan Event {
	ch := make(chan Event, 32)
	go func() {
		defer close(ch)
		emit := func(e Event) {
			if e.AgentName == "" {
				e.AgentName = name
			}
			ch <- e
		}
		out := fn(emit)
		switch out.Status {
		case StatusDone:
			emit(Event{Type: EventFinal, Response: out.Response, RunID: out.RunID})
		case StatusPaused:
			emit(Event{Type: EventPaused, Response: out.Response, RunID: out.RunID, Requirements: out.Requirements})
		default:
			emit(Event{Type: EventError, Response: out.Response, RunID: out.RunID, Err: out.Err})
		}
	}()
	return ch
}

// Info 实现 Agent。
func (c *Chain) Info() Info {
	var tools []llm.ToolDef
	for _, s := range c.Steps {
		if a := s.agent(); a != nil {
			tools = append(tools, a.Info().Tools...)
		}
	}
	return Info{Name: c.Name, Description: "sequential chain", Tools: tools}
}
