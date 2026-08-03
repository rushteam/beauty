package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rushteam/beauty/contrib/llm"
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

// WithAgentToolModel 指定子 agent 使用的模型(覆盖外层 Request 无模型时的默认)。
func WithAgentToolModel(model string) AgentToolOption {
	return func(c *AgentToolConfig) { c.Model = model }
}

// WithAgentToolSystem 给子 agent 追加/覆盖 system prompt。
func WithAgentToolSystem(system string) AgentToolOption {
	return func(c *AgentToolConfig) { c.System = system }
}

// AgentAsTool 把一个子 Agent 包装成可被父 agent 调用的 Tool。
// 模型传入 {"input":"..."},子 Agent 跑完后把终态文本作为工具结果返回。
// 子 Agent 自带 Tools/Approve/MaxSteps(若是 *Runner);与父共享 ctx(可取消)。
// sub 可为任意 Agent(*Runner / *Chain / *Team / 策略包装器),从而支持任意嵌套。
func AgentAsTool(name, description string, sub Agent, opts ...AgentToolOption) Tool {
	cfg := &AgentToolConfig{}
	for _, o := range opts {
		o(cfg)
	}
	if description == "" {
		description = "委托子 agent 处理子任务,返回其最终文本答复"
	}
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
		// 标注子 agent 的运行由本工具调用触发,便于其 Event 关联到父(见 WithTrigger)。
		resp, err := sub.Run(WithTrigger(ctx, TriggerToolCall, name), req)
		if err != nil {
			if resp != nil && resp.Content != "" {
				return resp.Content + "\n(error: " + err.Error() + ")", nil
			}
			return "", err
		}
		return resp.Content, nil
	})
}

// ChainStep 是 Chain 中的一步。
type ChainStep struct {
	Name   string // 可选,便于日志
	Agent  Agent  // 本步要跑的 agent(任意 Agent);为 nil 时回退用 Runner
	Runner *Runner
	Model  string // 非空则覆盖本步模型
	System string // 非空则覆盖本步 system
}

// agent 返回本步实际要跑的 Agent:优先 Agent 字段,回退旧的 Runner 字段。
func (s ChainStep) agent() Agent {
	if s.Agent != nil {
		return s.Agent
	}
	if s.Runner != nil {
		return s.Runner
	}
	return nil
}

// Chain 按序跑多个 agent:第 1 步使用调用方 Request;后续每步把上一步终态文本作为
// 唯一 user 消息(可带本步 System)。任一步出错即返回。ctx 取消可中止。
type Chain struct {
	Name  string // 可选,用于 Info()
	Steps []ChainStep
}

var _ StreamAgent = (*Chain)(nil)

// stepReq 根据链的当前输入与本步覆盖项,算出第 i 步要用的 Request。
func (c *Chain) stepReq(cur llm.Request, i int, prev *llm.Response) llm.Request {
	step := c.Steps[i]
	r := cur
	if step.Model != "" {
		r.Model = step.Model
	}
	if step.System != "" {
		r.System = step.System
	}
	if i > 0 {
		// 后续步骤:仅以上一步输出为输入,避免把整段历史重复塞给每个子 agent。
		r.Messages = []llm.Message{{Role: llm.User, Content: prev.Content}}
	}
	return r
}

// Run 顺序执行各步,返回最后一步的终态响应。
func (c *Chain) Run(ctx context.Context, req llm.Request) (*llm.Response, error) {
	if len(c.Steps) == 0 {
		return nil, fmt.Errorf("agent: empty chain")
	}
	var last *llm.Response
	for i := range c.Steps {
		if err := ctx.Err(); err != nil {
			return last, err
		}
		a := c.Steps[i].agent()
		if a == nil {
			return last, fmt.Errorf("agent: chain step %d (%s): nil agent", i, c.Steps[i].Name)
		}
		resp, err := a.Run(ctx, c.stepReq(req, i, last))
		if err != nil {
			return resp, err
		}
		last = resp
	}
	return last, nil
}

// RunStream 实现 StreamAgent:前 n-1 步同步跑,最后一步流式转发其 Event(若该步 agent 支持
// StreamAgent,否则退化为同步跑并只发一个 EventFinal)。任一前置步出错时发 EventError 收尾。
func (c *Chain) RunStream(ctx context.Context, req llm.Request) <-chan Event {
	ch := make(chan Event, 32)
	go func() {
		defer close(ch)
		if len(c.Steps) == 0 {
			ch <- Event{Type: EventError, AgentName: c.Name, Err: fmt.Errorf("agent: empty chain")}
			return
		}
		var last *llm.Response
		// 前 n-1 步同步跑。
		for i := 0; i < len(c.Steps)-1; i++ {
			if err := ctx.Err(); err != nil {
				ch <- Event{Type: EventError, AgentName: c.Name, Response: last, Err: err}
				return
			}
			a := c.Steps[i].agent()
			if a == nil {
				ch <- Event{Type: EventError, AgentName: c.Name, Response: last, Err: fmt.Errorf("agent: chain step %d (%s): nil agent", i, c.Steps[i].Name)}
				return
			}
			resp, err := a.Run(ctx, c.stepReq(req, i, last))
			if err != nil {
				ch <- Event{Type: EventError, AgentName: c.Name, Response: resp, Err: err}
				return
			}
			last = resp
		}
		// 最后一步:能流式就转发其事件,否则同步跑发 EventFinal。
		i := len(c.Steps) - 1
		lastReq := c.stepReq(req, i, last)
		a := c.Steps[i].agent()
		if a == nil {
			ch <- Event{Type: EventError, AgentName: c.Name, Response: last, Err: fmt.Errorf("agent: chain step %d (%s): nil agent", i, c.Steps[i].Name)}
			return
		}
		if sa, ok := a.(StreamAgent); ok {
			for ev := range sa.RunStream(ctx, lastReq) {
				ch <- ev
			}
			return
		}
		resp, err := a.Run(ctx, lastReq)
		if err != nil {
			ch <- Event{Type: EventError, AgentName: c.Name, Response: resp, Err: err}
			return
		}
		ch <- Event{Type: EventFinal, AgentName: c.Name, Response: resp}
	}()
	return ch
}

// Info 实现 Agent:名字取 Chain.Name,工具汇总各步 agent 暴露的工具声明。
func (c *Chain) Info() Info {
	var tools []llm.ToolDef
	for _, s := range c.Steps {
		if a := s.agent(); a != nil {
			tools = append(tools, a.Info().Tools...)
		}
	}
	return Info{Name: c.Name, Description: "sequential chain", Tools: tools}
}
