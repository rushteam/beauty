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

// AgentAsTool 把一个子 Runner 包装成可被父 agent 调用的 Tool。
// 模型传入 {"input":"..."},子 Runner 跑完后把终态文本作为工具结果返回。
// 子 Runner 自带 Tools/Approve/MaxSteps;与父共享 ctx(可取消)。
func AgentAsTool(name, description string, sub *Runner, opts ...AgentToolOption) Tool {
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
		resp, err := sub.Run(ctx, req)
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
	Runner *Runner
	Model  string // 非空则覆盖本步模型
	System string // 非空则覆盖本步 system
}

// Chain 按序跑多个 Runner:第 1 步使用调用方 Request;后续每步把上一步终态文本作为
// 唯一 user 消息(可带本步 System)。任一步出错即返回。ctx 取消可中止。
type Chain struct {
	Steps []ChainStep
}

// Run 顺序执行各步,返回最后一步的终态响应。
func (c *Chain) Run(ctx context.Context, req llm.Request) (*llm.Response, error) {
	if len(c.Steps) == 0 {
		return nil, fmt.Errorf("agent: empty chain")
	}
	cur := req
	var last *llm.Response
	for i, step := range c.Steps {
		if err := ctx.Err(); err != nil {
			return last, err
		}
		if step.Runner == nil {
			return last, fmt.Errorf("agent: chain step %d (%s): nil runner", i, step.Name)
		}
		stepReq := cur
		if step.Model != "" {
			stepReq.Model = step.Model
		}
		if step.System != "" {
			stepReq.System = step.System
		}
		if i > 0 {
			// 后续步骤:仅以上一步输出为输入,避免把整段历史重复塞给每个子 agent。
			stepReq.Messages = []llm.Message{{Role: llm.User, Content: last.Content}}
		}
		resp, err := step.Runner.Run(ctx, stepReq)
		if err != nil {
			return resp, err
		}
		last = resp
	}
	return last, nil
}
