// Package agent 是 beauty 在 contrib/llm 之上的**薄 agent 循环机制**:给定一个 llm.Client 和
// 一组可执行工具,自动跑"模型→请求调用工具→执行→把结果喂回→再让模型继续"的循环,直到模型
// 不再要求调用工具(得到终态文本回复)或到达步数上限。纯标准库,只依赖 contrib/llm 的类型。
//
// 边界(机制而非策略):
//   - prompt、选哪个模型、温度、给哪些工具、要不要人工审批 —— 都是 policy,由使用方在 Request/Tools 里定;
//   - 本包只负责"循环 + 分发工具 + 拼装消息",不内置任何具体工具;
//   - 工具来源与本包解耦:Tool.Call 就是普通 Go 函数,把 contrib/mcp 的远程工具、本地函数、HTTP
//     调用等适配成 Tool 只需几行(见 example),故本包不 import mcp,保持零外部依赖。
//
// v1 只走非流式(Client.Generate)循环:工具往返需要完整的 ToolCall 才能执行,流式分片拼装
// 留作后续。最终文本如需流式,可在拿到终态后由调用方自行再发一次 Stream。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/rushteam/beauty/contrib/llm"
)

// Tool 是一个可被模型调用的工具:Def 是给模型看的声明(名字/描述/入参 schema),
// Call 是实际执行——收到模型给的入参(JSON),返回喂回模型的文本结果。
// Call 返回 error 时,错误信息会作为工具结果回传给模型(让它自行重试或纠正),而不是中断整个循环。
//
// Approval=true 表示该工具是敏感操作,执行前需经 Runner.Approve 人工确认(未设 Approve 时照常执行)。
type Tool struct {
	Def      llm.ToolDef
	Call     func(ctx context.Context, args json.RawMessage) (string, error)
	Approval bool
}

// Func 是构造 Tool 的便捷函数。
func Func(name, description string, parameters json.RawMessage, call func(context.Context, json.RawMessage) (string, error)) Tool {
	return Tool{Def: llm.ToolDef{Name: name, Description: description, Parameters: parameters}, Call: call}
}

// ErrMaxSteps 表示循环到达 MaxSteps 仍未得到终态回复(模型还在要求调用工具)。
// 返回时会同时带上最后一次的 *llm.Response,便于调用方观察。
var ErrMaxSteps = errors.New("agent: reached max steps without final response")

// DefaultMaxSteps 是未设置 Runner.MaxSteps 时的默认步数上限。
const DefaultMaxSteps = 8

// Decision 是一次人工审批的结果。Approved=false 时 Reason 会作为拒绝理由喂回模型。
type Decision struct {
	Approved bool
	Reason   string
}

// Runner 驱动 agent 循环。Client 可以是任意 llm.Client(含 Fallback/Retry/Metered/Guard 叠加后的)。
type Runner struct {
	Client   llm.Client
	Tools    []Tool
	MaxSteps int // <=0 时用 DefaultMaxSteps

	// OnStep 在每次模型返回后回调(step 从 1 起),用于埋点/日志/观察工具调用。可为 nil。
	OnStep func(step int, resp *llm.Response)

	// Approve 是工具级人工审批门:执行标记 Approval 的工具前调用。返回 Approved=false → 拒绝理由
	// 喂回模型继续;返回 error → 视为审批失败,中止整个 Run。为 nil 时,带 Approval 的工具照常执行
	// (即未启用审批)。实现可阻塞等待人工确认(如从 channel/HTTP 拿决定)。
	Approve func(ctx context.Context, tc llm.ToolCall) (Decision, error)
}

// Run 跑完整的工具循环并返回终态响应。req 里带 Model / Messages(system 用 Request.System 或
// 一条 system 消息)/ 温度等;Runner 会自动注入 Tools 并逐轮追加 assistant/tool 消息。
// req.Messages 不会被就地修改(内部使用副本)。
func (r *Runner) Run(ctx context.Context, req llm.Request) (*llm.Response, error) {
	maxSteps := r.MaxSteps
	if maxSteps <= 0 {
		maxSteps = DefaultMaxSteps
	}

	// 工具声明与按名索引(一次构建,循环复用)。
	defs := make([]llm.ToolDef, len(r.Tools))
	byName := make(map[string]Tool, len(r.Tools))
	for i, t := range r.Tools {
		defs[i] = t.Def
		byName[t.Def.Name] = t
	}
	req.Tools = defs

	// 复制消息,避免修改调用方切片。
	msgs := make([]llm.Message, len(req.Messages))
	copy(msgs, req.Messages)

	var last *llm.Response
	for step := 1; step <= maxSteps; step++ {
		req.Messages = msgs
		resp, err := r.Client.Generate(ctx, req)
		if err != nil {
			return last, err
		}
		last = resp
		if r.OnStep != nil {
			r.OnStep(step, resp)
		}

		// 无工具调用 → 终态,返回。
		if len(resp.ToolCalls) == 0 {
			return resp, nil
		}

		// 记录 assistant 这一回合(含它请求的工具调用),再逐个执行、把结果作为 tool 消息追加。
		msgs = append(msgs, llm.Message{Role: llm.Assistant, Content: resp.Content, ToolCalls: resp.ToolCalls})
		for _, tc := range resp.ToolCalls {
			result, fatal := r.dispatch(ctx, byName, tc)
			if fatal != nil { // 审批失败等致命错误:中止整个 Run
				return last, fatal
			}
			msgs = append(msgs, llm.Message{Role: llm.Tool, ToolCallID: tc.ID, Content: result})
		}
	}
	return last, ErrMaxSteps
}

// dispatch 执行一次工具调用,返回喂回模型的文本结果。未知工具、被拒绝、执行出错都转成文本回传,
// 让模型有机会自行纠正,而不中断循环;仅审批门本身出错(fatal 非 nil)才中止 Run。
func (r *Runner) dispatch(ctx context.Context, byName map[string]Tool, tc llm.ToolCall) (result string, fatal error) {
	t, ok := byName[tc.Name]
	if !ok {
		return fmt.Sprintf("error: unknown tool %q", tc.Name), nil
	}
	if t.Approval && r.Approve != nil {
		dec, err := r.Approve(ctx, tc)
		if err != nil {
			return "", fmt.Errorf("agent: approval for %q: %w", tc.Name, err)
		}
		if !dec.Approved {
			msg := fmt.Sprintf("工具 %q 的调用被拒绝", tc.Name)
			if dec.Reason != "" {
				msg += ": " + dec.Reason
			}
			return msg, nil
		}
	}
	out, err := t.Call(ctx, tc.Arguments)
	if err != nil {
		return "error: " + err.Error(), nil
	}
	return out, nil
}
