package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
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
		out := CollectOutcome(sub.Run(childCtx, req))
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
				Resume: func(ctx context.Context, resolutions []Resolution, opts ...Option) iter.Seq2[Event, error] {
					return sub.Continue(ctx, childID, resolutions, opts...)
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

var _ Agent = (*Chain)(nil)

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

func (c *Chain) cp() OrchestratorCheckpoint {
	return OrchestratorCheckpoint{Store: c.Store, Name: c.Name}
}

// LoadRunTree 从 checkpoint 事件日志构建编排树。
func (c *Chain) LoadRunTree(ctx context.Context, runID string) (*checkpoint.RunNode, error) {
	c.ensureStore()
	return LoadRunTreeFromStore(ctx, c.Store, runID)
}

// LoadUIEvents 读取 run 的全部 checkpoint 事件。
func (c *Chain) LoadUIEvents(ctx context.Context, runID string) ([]checkpoint.Event, error) {
	c.ensureStore()
	return LoadUIEventsFromStore(ctx, c.Store, runID)
}

func (c *Chain) stepSource(i int) string {
	if c.Steps[i].Name != "" {
		return "chain:" + c.Steps[i].Name
	}
	return fmt.Sprintf("chain:%d", i)
}

// Run 顺序执行各步,返回事件流。
func (c *Chain) Run(ctx context.Context, req llm.Request, opts ...Option) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		c.ensureStore()
		out := c.runFrom(ctx, newRunID(), req, 0, "", yield, opts...)
		switch out.Status {
		case StatusDone:
			yield(Event{Type: EventFinal, Response: out.Response, RunID: out.RunID, AgentName: c.Name}, nil)
		case StatusPaused:
			yield(Event{Type: EventPaused, Response: out.Response, RunID: out.RunID, Requirements: out.Requirements, AgentName: c.Name}, nil)
		default:
			yield(Event{Type: EventError, Response: out.Response, RunID: out.RunID, Err: out.Err, AgentName: c.Name}, out.Err)
		}
	}
}

// Continue 从暂停步恢复。
func (c *Chain) Continue(ctx context.Context, runID string, resolutions []Resolution, opts ...Option) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		c.ensureStore()
		out := c.continueSync(ctx, runID, resolutions, yield, opts...)
		switch out.Status {
		case StatusDone:
			yield(Event{Type: EventFinal, Response: out.Response, RunID: out.RunID, AgentName: c.Name}, nil)
		case StatusPaused:
			yield(Event{Type: EventPaused, Response: out.Response, RunID: out.RunID, Requirements: out.Requirements, AgentName: c.Name}, nil)
		default:
			yield(Event{Type: EventError, Response: out.Response, RunID: out.RunID, Err: out.Err, AgentName: c.Name}, out.Err)
		}
	}
}

func (c *Chain) continueSync(ctx context.Context, runID string, resolutions []Resolution, emit func(Event, error) bool, opts ...Option) RunOutcome {
	cp := c.cp()
	cp.Resumed(ctx, runID)
	snap, err := loadSnapshotFromStore(ctx, c.Store, runID)
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
	out := collectMemberContinue(ctx, cr.agent, cr.childID, resolutions, func(e Event) {
		e.AgentName = c.Name
		emit(e, nil)
	}, opts...)
	switch out.Status {
	case StatusPaused:
		reqs := remapRequirements(out.Requirements, snap.ChildSource)
		snap.Requirements = reqs
		snap.ChildRunID = out.RunID
		c.resumes.Store(runID, chainResume{agent: cr.agent, childID: out.RunID})
		if err := saveSnapshotWithCheckpoint(ctx, c.Store, runID, snap); err != nil {
			return outcomeError(runID, out.Response, nil, err)
		}
		cp.Paused(ctx, runID, snap.ChainStep, out.Response, reqs, out.RunID, snap.ChildSource)
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
		return c.runFrom(ctx, runID, snap.Request, snap.ChainStep+1, content, emit, opts...)
	default:
		return outcomeError(runID, out.Response, out.Messages, fmt.Errorf("agent: chain unexpected status %q", out.Status))
	}
}

func (c *Chain) runFrom(ctx context.Context, runID string, req llm.Request, start int, prevContent string, emit func(Event, error) bool, opts ...Option) RunOutcome {
	if len(c.Steps) == 0 {
		return outcomeError(runID, nil, nil, fmt.Errorf("agent: empty chain"))
	}
	cp := c.cp()
	if start == 0 {
		cp.Started(ctx, runID, req)
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
		out := collectMemberRun(ctx, a, c.stepReq(req, i, content), func(e Event) {
			e.AgentName = c.Name
			emit(e, nil)
		}, opts...)
		src := c.stepSource(i)
		cp.Spawned(ctx, runID, out.RunID, src, i)
		switch out.Status {
		case StatusPaused:
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
			if err := saveSnapshotWithCheckpoint(ctx, c.Store, runID, snap); err != nil {
				return outcomeError(runID, out.Response, nil, err)
			}
			cp.Paused(ctx, runID, i, out.Response, reqs, out.RunID, src)
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
	cp.Completed(ctx, runID)
	_ = c.Store.Delete(ctx, runID)
	return outcomeDone(runID, last, nil)
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
