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
// 支持 Run(同步)与 RunStream(事件流,ctx 可取消);工具权限三态 Allow/Ask/Deny;
// AgentAsTool / Chain 做薄多 agent 编排;Steer 中途插话;Hooks 分层观测。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/rushteam/beauty/contrib/llm"
)

// Permission 是工具调用权限三态。
type Permission int

const (
	// PermitAllow 直接执行(默认)。
	PermitAllow Permission = iota
	// PermitAsk 执行前经 Runner.Approve 人工确认。
	PermitAsk
	// PermitDeny 策略拒绝,不执行;拒绝说明喂回模型。
	PermitDeny
)

// Tool 是一个可被模型调用的工具:Def 是给模型看的声明(名字/描述/入参 schema),
// Call 是实际执行——收到模型给的入参(JSON),返回喂回模型的文本结果。
// Call 返回 error 时,错误信息会作为工具结果回传给模型(让它自行重试或纠正),而不是中断整个循环。
//
// Permission 控制是否可执行;Approval=true 是旧字段,等价于 Permission=PermitAsk
// (仅当 Permission 仍为默认 Allow 时生效,便于兼容存量代码)。
type Tool struct {
	Def        llm.ToolDef
	Call       func(ctx context.Context, args json.RawMessage) (string, error)
	Permission Permission
	Approval   bool // deprecated: use Permission=PermitAsk
}

// Func 是构造 Tool 的便捷函数(默认 PermitAllow)。
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

// EventType 标识 RunStream 中的事件种类。
type EventType string

const (
	EventStep       EventType = "step"        // 模型返回一轮(可能含 tool_calls)
	EventToolStart  EventType = "tool_start"  // 即将执行工具
	EventToolResult EventType = "tool_result" // 工具执行完毕(含拒绝/错误文本)
	EventSteer      EventType = "steer"       // 中途注入的用户消息(Result 为文本)
	EventFinal      EventType = "final"       // 终态文本回复
	EventError      EventType = "error"       // 循环失败(含 MaxSteps / 审批失败 / ctx 取消)
)

// Event 是 RunStream 推送的一条事件。字段按 Type 选用:
//   - step/final: Response
//   - tool_start / tool_result: ToolCall (+ Result 仅 result)
//   - steer: Result 为注入的用户文本
//   - error: Err (+ 可选 Response 为最后一次模型输出)
type Event struct {
	Type     EventType
	Step     int
	Response *llm.Response
	ToolCall *llm.ToolCall
	Result   string
	Err      error
}

// Hooks 是分层观测/拦截点。任一回调返回 error 会中止整个 Run。
// 字段均可为 nil。比单一 OnStep 更细:可改请求、拦工具、记指标。
type Hooks struct {
	BeforeModel func(ctx context.Context, step int, req *llm.Request) error
	AfterModel  func(ctx context.Context, step int, resp *llm.Response) error
	BeforeTool  func(ctx context.Context, step int, tc llm.ToolCall) error
	AfterTool   func(ctx context.Context, step int, tc llm.ToolCall, result string) error
}

// Steer 是中途插话信箱:外部 Enqueue 的文本会在「下一轮 Generate 之前」注入为 user 消息
// (通常发生在本轮工具全部跑完之后)。并发安全;信箱满时 Enqueue 非阻塞丢弃并返回 false。
type Steer struct {
	ch chan string
}

// NewSteer 创建插话信箱。buf<=0 时用 8。
func NewSteer(buf int) *Steer {
	if buf < 1 {
		buf = 8
	}
	return &Steer{ch: make(chan string, buf)}
}

// Enqueue 投入一条用户插话。steer 为 nil 或 msg 为空则忽略;信箱满返回 false。
func (s *Steer) Enqueue(msg string) bool {
	if s == nil || msg == "" {
		return false
	}
	select {
	case s.ch <- msg:
		return true
	default:
		return false
	}
}

func (s *Steer) drain() []string {
	if s == nil {
		return nil
	}
	var out []string
	for {
		select {
		case m := <-s.ch:
			out = append(out, m)
		default:
			return out
		}
	}
}

// Runner 驱动 agent 循环。Client 可以是任意 llm.Client(含 Fallback/Retry/Metered/Guard 叠加后的)。
type Runner struct {
	Client   llm.Client
	Tools    []Tool
	MaxSteps int // <=0 时用 DefaultMaxSteps

	// OnStep 在每次模型返回后回调(step 从 1 起),用于埋点/日志/观察工具调用。可为 nil。
	// 更细的观测请用 Hooks;OnStep 仍会触发以保持兼容。
	OnStep func(step int, resp *llm.Response)

	// Approve 是工具级人工审批门:执行 PermitAsk(或旧 Approval)工具前调用。
	// 返回 Approved=false → 拒绝理由喂回模型继续;返回 error → 中止整个 Run。
	// 为 nil 时,Ask 工具仍会执行(未启用审批)。实现可阻塞等待人工确认。
	Approve func(ctx context.Context, tc llm.ToolCall) (Decision, error)

	// Hooks 分层回调(Before/After × Model/Tool)。可为零值。
	Hooks Hooks

	// Steer 中途插话信箱。可为 nil(不启用)。
	Steer *Steer
}

// Run 跑完整的工具循环并返回终态响应。ctx 取消会中止循环。
// req.Messages 不会被就地修改(内部使用副本)。
func (r *Runner) Run(ctx context.Context, req llm.Request) (*llm.Response, error) {
	return r.run(ctx, req, nil)
}

// RunStream 异步跑循环,通过 channel 推送 Event。channel 在结束时关闭。
// ctx 取消会尽快停止(当前 Generate/工具调用依赖其 ctx),并推送 EventError。
// 调用方应排空 channel 直至关闭,以免泄漏 goroutine。
func (r *Runner) RunStream(ctx context.Context, req llm.Request) <-chan Event {
	ch := make(chan Event, 16)
	go func() {
		defer close(ch)
		emit := func(e Event) { ch <- e }
		resp, err := r.run(ctx, req, emit)
		if err != nil {
			emit(Event{Type: EventError, Response: resp, Err: err})
			return
		}
		emit(Event{Type: EventFinal, Response: resp})
	}()
	return ch
}

func (r *Runner) run(ctx context.Context, req llm.Request, emit func(Event)) (*llm.Response, error) {
	maxSteps := r.MaxSteps
	if maxSteps <= 0 {
		maxSteps = DefaultMaxSteps
	}

	defs := make([]llm.ToolDef, len(r.Tools))
	byName := make(map[string]Tool, len(r.Tools))
	for i, t := range r.Tools {
		defs[i] = t.Def
		byName[t.Def.Name] = t
	}
	req.Tools = defs

	msgs := make([]llm.Message, len(req.Messages))
	copy(msgs, req.Messages)

	var last *llm.Response
	for step := 1; step <= maxSteps; step++ {
		if err := ctx.Err(); err != nil {
			return last, err
		}

		// 工具边界 / 步间:注入中途插话
		for _, m := range r.Steer.drain() {
			msgs = append(msgs, llm.Message{Role: llm.User, Content: m})
			if emit != nil {
				emit(Event{Type: EventSteer, Step: step, Result: m})
			}
		}

		req.Messages = msgs
		if r.Hooks.BeforeModel != nil {
			if err := r.Hooks.BeforeModel(ctx, step, &req); err != nil {
				return last, err
			}
			msgs = req.Messages // 允许钩子改 Messages
		}

		resp, err := r.Client.Generate(ctx, req)
		if err != nil {
			return last, err
		}
		last = resp
		if r.Hooks.AfterModel != nil {
			if err := r.Hooks.AfterModel(ctx, step, resp); err != nil {
				return last, err
			}
		}
		if r.OnStep != nil {
			r.OnStep(step, resp)
		}
		if emit != nil {
			emit(Event{Type: EventStep, Step: step, Response: resp})
		}

		if len(resp.ToolCalls) == 0 {
			return resp, nil
		}

		msgs = append(msgs, llm.Message{Role: llm.Assistant, Content: resp.Content, ToolCalls: resp.ToolCalls})
		for _, tc := range resp.ToolCalls {
			if err := ctx.Err(); err != nil {
				return last, err
			}
			tc := tc
			if r.Hooks.BeforeTool != nil {
				if err := r.Hooks.BeforeTool(ctx, step, tc); err != nil {
					return last, err
				}
			}
			if emit != nil {
				emit(Event{Type: EventToolStart, Step: step, ToolCall: &tc})
			}
			result, fatal := r.dispatch(ctx, byName, tc)
			if emit != nil {
				emit(Event{Type: EventToolResult, Step: step, ToolCall: &tc, Result: result})
			}
			if r.Hooks.AfterTool != nil {
				if err := r.Hooks.AfterTool(ctx, step, tc, result); err != nil {
					return last, err
				}
			}
			if fatal != nil {
				return last, fatal
			}
			msgs = append(msgs, llm.Message{Role: llm.Tool, ToolCallID: tc.ID, Content: result})
		}
	}
	return last, ErrMaxSteps
}

func (t Tool) effectivePerm() Permission {
	if t.Permission != PermitAllow {
		return t.Permission
	}
	if t.Approval {
		return PermitAsk
	}
	return PermitAllow
}

// dispatch 执行一次工具调用,返回喂回模型的文本结果。未知工具、被拒绝、执行出错都转成文本回传,
// 让模型有机会自行纠正,而不中断循环;仅审批门本身出错(fatal 非 nil)才中止 Run。
func (r *Runner) dispatch(ctx context.Context, byName map[string]Tool, tc llm.ToolCall) (result string, fatal error) {
	t, ok := byName[tc.Name]
	if !ok {
		return fmt.Sprintf("error: unknown tool %q", tc.Name), nil
	}
	switch t.effectivePerm() {
	case PermitDeny:
		return fmt.Sprintf("工具 %q 被策略拒绝(deny),不可调用", tc.Name), nil
	case PermitAsk:
		if r.Approve != nil {
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
	}
	out, err := t.Call(ctx, tc.Arguments)
	if err != nil {
		return "error: " + err.Error(), nil
	}
	return out, nil
}
