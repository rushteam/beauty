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
// 支持 Run(同步 Generate)与 RunStream(Stream 推 EventToken + 步骤事件,ctx 可取消);
// 同轮多 tool 默认可并行;工具权限三态;AgentAsTool / Chain;Steer;Hooks。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

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

// TriggerType 标识一次 agent 运行(及其发出的 Event)由何种事件触发,用于多 agent 场景下的父子归因。
type TriggerType string

const (
	// TriggerUser 顶层用户请求(默认)。
	TriggerUser TriggerType = "user"
	// TriggerToolCall 由父 agent 的某次工具调用触发(如 AgentAsTool)。
	TriggerToolCall TriggerType = "tool_call"
	// TriggerTransfer 由多 agent 移交(handoff/transfer)触发。
	TriggerTransfer TriggerType = "transfer"
)

// triggerCtxKey 是 WithTrigger 在 ctx 上存放触发信息的键(零大小类型,避免碰撞)。
type triggerCtxKey struct{}

// WithTrigger 在 ctx 上标注「本次 agent 运行由何种事件触发、关联哪个 id」,
// 使该运行发出的每条 Event 都带上 TriggerType/TriggerID。编排点(AgentAsTool / Team)在调用
// 子 agent 前设置;顶层调用未设置时默认为 TriggerUser。
func WithTrigger(ctx context.Context, tt TriggerType, id string) context.Context {
	return context.WithValue(ctx, triggerCtxKey{}, [2]string{string(tt), id})
}

func triggerFrom(ctx context.Context) (TriggerType, string) {
	if v, ok := ctx.Value(triggerCtxKey{}).([2]string); ok {
		return TriggerType(v[0]), v[1]
	}
	return TriggerUser, ""
}

// EventType 标识 RunStream 中的事件种类。
type EventType string

const (
	EventToken      EventType = "token"       // 模型文本增量(Result=delta)
	EventStep       EventType = "step"        // 模型一轮完成(Response 含完整内容/tool_calls)
	EventToolStart  EventType = "tool_start"  // 即将执行工具
	EventToolResult EventType = "tool_result" // 工具执行完毕(含拒绝/错误文本)
	EventSteer      EventType = "steer"       // 中途注入的用户消息(Result 为文本)
	EventFinal      EventType = "final"       // 终态文本回复
	EventError      EventType = "error"       // 循环失败(含 MaxSteps / 审批失败 / ctx 取消)
)

// Event 是 RunStream 推送的一条事件。字段按 Type 选用:
//   - token: Result=增量文本
//   - step/final: Response
//   - tool_start / tool_result: ToolCall (+ Result 仅 result)
//   - steer: Result 为注入的用户文本
//   - error: Err (+ 可选 Response 为最后一次模型输出)
//
// AgentName / TriggerType / TriggerID 由 Runner 统一打上:AgentName 为发出事件的 agent 名
// (Runner.Name);TriggerType/TriggerID 取自 ctx(见 WithTrigger),顶层默认 TriggerUser/""。
type Event struct {
	Type     EventType
	Step     int
	Response *llm.Response
	ToolCall *llm.ToolCall
	Result   string
	Err      error

	// 父子关联元数据(多 agent 场景):
	AgentName   string      // 产生该事件的 agent 名(Runner.Name)
	TriggerType TriggerType // 触发本次运行的事件种类
	TriggerID   string      // 关联 id(如触发的 tool 名 / 移交目标)
}

// Hooks 是分层观测/拦截点。任一回调返回 error 会中止整个 Run。
// 字段均可为 nil。并行工具时 Before/AfterTool 可能并发调用,实现需自备同步。
type Hooks struct {
	BeforeModel func(ctx context.Context, step int, req *llm.Request) error
	AfterModel  func(ctx context.Context, step int, resp *llm.Response) error
	BeforeTool  func(ctx context.Context, step int, tc llm.ToolCall) error
	AfterTool   func(ctx context.Context, step int, tc llm.ToolCall, result string) error
}

// Steer 是中途插话信箱:外部 Enqueue 的文本会在「下一轮模型调用之前」注入为 user 消息
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

// Info 是一个 Agent 的元信息(名字/描述/对外暴露的工具声明),供编排与观测使用。
type Info struct {
	Name        string
	Description string
	Tools       []llm.ToolDef
}

// Agent 是可被统一编排的最小契约:给定 Request 跑完并返回终态 Response,并能自述 Info。
// *Runner、*Chain、策略包装器(*BestOfN / *VerifyLoop)、*Team 都实现它,从而可互相嵌套
// (AgentAsTool 包任意 Agent、Chain 步骤为任意 Agent、Team 成员为任意 Agent)。
type Agent interface {
	Run(ctx context.Context, req llm.Request) (*llm.Response, error)
	Info() Info
}

// StreamAgent 是额外支持流式事件的 Agent。*Runner 与 *Chain 实现它;
// 语义上无单一事件流的包装器(如 *BestOfN 的 N 路并行)只实现 Agent。
type StreamAgent interface {
	Agent
	RunStream(ctx context.Context, req llm.Request) <-chan Event
}

// Runner 驱动 agent 循环。Client 可以是任意 llm.Client(含 Fallback/Retry/Metered/Guard 叠加后的)。
type Runner struct {
	Client   llm.Client
	Tools    []Tool
	MaxSteps int // <=0 时用 DefaultMaxSteps

	// Name / Description 是本 agent 的元信息(供 Info() 与多 agent 编排/观测),均可为空。
	Name        string
	Description string

	// Planner 可选:在首轮模型调用前注入规划指令,并对每轮响应做后处理(如 ReAct)。nil=不启用。
	Planner Planner

	// ParallelTools 控制同轮多个 tool_call 是否并发执行。nil=默认并行(>1 时);
	// 指向 false 则强制串行。并行时 Hooks.Before/AfterTool 与 Approve 可能并发,需自行同步。
	ParallelTools *bool

	// OnStep 在每次模型返回后回调(step 从 1 起),用于埋点/日志/观察工具调用。可为 nil。
	OnStep func(step int, resp *llm.Response)

	// Approve 是工具级人工审批门:执行 PermitAsk(或旧 Approval)工具前调用。
	Approve func(ctx context.Context, tc llm.ToolCall) (Decision, error)

	// Hooks 分层回调(Before/After × Model/Tool)。可为零值。
	Hooks Hooks

	// Steer 中途插话信箱。可为 nil(不启用)。
	Steer *Steer
}

var _ StreamAgent = (*Runner)(nil)

// Info 实现 Agent:返回本 Runner 的名字/描述与其工具声明。
func (r *Runner) Info() Info {
	defs := make([]llm.ToolDef, len(r.Tools))
	for i, t := range r.Tools {
		defs[i] = t.Def
	}
	return Info{Name: r.Name, Description: r.Description, Tools: defs}
}

// Run 跑完整的工具循环并返回终态响应(内部走 Client.Generate)。ctx 取消会中止循环。
func (r *Runner) Run(ctx context.Context, req llm.Request) (*llm.Response, error) {
	return r.run(ctx, req, nil)
}

// RunStream 异步跑循环:模型侧走 Client.Stream,推送 EventToken 增量,并推送步骤/工具事件。
// provider 若不支持流式 tool_calls(Done 时无 ToolCalls 且内容为空),会回退到 Generate。
// channel 在结束时关闭;调用方应排空直至关闭。
func (r *Runner) RunStream(ctx context.Context, req llm.Request) <-chan Event {
	ch := make(chan Event, 32)
	go func() {
		defer close(ch)
		// 统一给每条 Event 打上 AgentName 与来自 ctx 的触发元数据(TriggerType/TriggerID)。
		tt, tid := triggerFrom(ctx)
		emit := func(e Event) {
			e.AgentName = r.Name
			e.TriggerType = tt
			e.TriggerID = tid
			ch <- e
		}
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
	// 注:Event 的 AgentName/Trigger 元数据统一由 RunStream 的 emit 打上(Run 走 nil emit,无事件)。
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

	// 规划器:进循环前把规划指令一次性并入 system(后续各轮复用同一 req.System)。
	if r.Planner != nil {
		if instr := r.Planner.BuildPlanningInstruction(&req); instr != "" {
			if req.System != "" {
				req.System += "\n\n"
			}
			req.System += instr
		}
	}

	msgs := make([]llm.Message, len(req.Messages))
	copy(msgs, req.Messages)

	var last *llm.Response
	for step := 1; step <= maxSteps; step++ {
		if err := ctx.Err(); err != nil {
			return last, err
		}

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
			msgs = req.Messages
		}

		resp, err := r.callModel(ctx, req, step, emit)
		if err != nil {
			return last, err
		}
		if r.Planner != nil {
			resp = r.Planner.ProcessPlanningResponse(step, resp)
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
		toolMsgs, fatal := r.runTools(ctx, step, byName, resp.ToolCalls, emit)
		if fatal != nil {
			return last, fatal
		}
		msgs = append(msgs, toolMsgs...)
	}
	return last, ErrMaxSteps
}

// callModel:Run 用 Generate;RunStream 用 Stream 推 token,必要时回退 Generate。
func (r *Runner) callModel(ctx context.Context, req llm.Request, step int, emit func(Event)) (*llm.Response, error) {
	if emit == nil {
		return r.Client.Generate(ctx, req)
	}
	ch, err := r.Client.Stream(ctx, req)
	if err != nil {
		return r.Client.Generate(ctx, req)
	}
	var content strings.Builder
	var toolCalls []llm.ToolCall
	var usage llm.Usage
	for c := range ch {
		if c.Err != nil {
			return nil, c.Err
		}
		if c.Delta != "" {
			content.WriteString(c.Delta)
			emit(Event{Type: EventToken, Step: step, Result: c.Delta})
		}
		if len(c.ToolCalls) > 0 {
			toolCalls = c.ToolCalls
		}
		if c.Usage != nil {
			usage = *c.Usage
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resp := &llm.Response{Content: content.String(), ToolCalls: toolCalls, Usage: usage, Model: req.Model}
	// provider 未给出流式 tool_calls、又没有任何文本,而请求声明了工具 → 回退 Generate
	if len(req.Tools) > 0 && len(toolCalls) == 0 && content.Len() == 0 {
		return r.Client.Generate(ctx, req)
	}
	return resp, nil
}

type toolOutcome struct {
	tc     llm.ToolCall
	result string
	fatal  error
}

func (r *Runner) parallelEnabled(n int) bool {
	if n <= 1 {
		return false
	}
	if r.ParallelTools != nil {
		return *r.ParallelTools
	}
	return true
}

func (r *Runner) runTools(ctx context.Context, step int, byName map[string]Tool, tcs []llm.ToolCall, emit func(Event)) ([]llm.Message, error) {
	if !r.parallelEnabled(len(tcs)) {
		var msgs []llm.Message
		for _, tc := range tcs {
			if err := ctx.Err(); err != nil {
				return msgs, err
			}
			out := r.execOne(ctx, step, byName, tc, emit)
			if out.fatal != nil {
				return msgs, out.fatal
			}
			msgs = append(msgs, llm.Message{Role: llm.Tool, ToolCallID: tc.ID, Content: out.result})
		}
		return msgs, nil
	}

	outs := make([]toolOutcome, len(tcs))
	var wg sync.WaitGroup
	for i, tc := range tcs {
		wg.Add(1)
		go func(i int, tc llm.ToolCall) {
			defer wg.Done()
			outs[i] = r.execOne(ctx, step, byName, tc, emit)
		}(i, tc)
	}
	wg.Wait()

	msgs := make([]llm.Message, 0, len(tcs))
	for i, out := range outs {
		if out.fatal != nil {
			return msgs, out.fatal
		}
		msgs = append(msgs, llm.Message{Role: llm.Tool, ToolCallID: tcs[i].ID, Content: out.result})
	}
	return msgs, nil
}

func (r *Runner) execOne(ctx context.Context, step int, byName map[string]Tool, tc llm.ToolCall, emit func(Event)) toolOutcome {
	if r.Hooks.BeforeTool != nil {
		if err := r.Hooks.BeforeTool(ctx, step, tc); err != nil {
			return toolOutcome{tc: tc, fatal: err}
		}
	}
	if emit != nil {
		tc := tc
		emit(Event{Type: EventToolStart, Step: step, ToolCall: &tc})
	}
	result, fatal := r.dispatch(ctx, byName, tc)
	if fatal != nil {
		return toolOutcome{tc: tc, result: result, fatal: fatal}
	}
	if emit != nil {
		tc := tc
		emit(Event{Type: EventToolResult, Step: step, ToolCall: &tc, Result: result})
	}
	if r.Hooks.AfterTool != nil {
		if err := r.Hooks.AfterTool(ctx, step, tc, result); err != nil {
			return toolOutcome{tc: tc, result: result, fatal: err}
		}
	}
	return toolOutcome{tc: tc, result: result}
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

func (r *Runner) dispatch(ctx context.Context, byName map[string]Tool, tc llm.ToolCall) (result string, fatal error) {
	t, ok := byName[tc.Name]
	if !ok {
		return fmt.Sprintf("error: unknown tool %q", tc.Name), nil
	}
	switch t.effectivePerm() {
	case PermitDeny:
		return fmt.Sprintf("工具 %q 被策略拒绝(deny),不可调用", tc.Name), nil
	case PermitAsk:
		if r.Approve == nil {
			return fmt.Sprintf("工具 %q 需要人工审批但未配置 Approve 回调,拒绝执行", tc.Name), nil
		}
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

// Bool 返回 *bool,便于设置 ParallelTools。
func Bool(v bool) *bool { return &v }
