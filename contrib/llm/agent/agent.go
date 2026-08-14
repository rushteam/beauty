// Package agent 是 beauty 在 contrib/llm 之上的薄 agent 循环:模型↔工具循环,直到终态、
// 步数上限或 PermitAsk 原子暂停。Run/Continue 返回统一 RunOutcome(done|paused|error);
// 审批不进 Runner 核心(产品路径显式 Continue;阻塞审批用外置 SyncHITL)。
//
// Runner / Chain / Team / Parallel / BestOfN / VerifyLoop 实现同一 Agent 契约,可互相嵌套。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent/checkpoint"
)

// Permission 是工具调用权限三态。
type Permission int

const (
	// PermitAllow 直接执行(默认)。
	PermitAllow Permission = iota
	// PermitAsk 本轮若出现则整轮原子暂停,待 Continue 决议后再执行。
	PermitAsk
	// PermitDeny 策略拒绝,不执行;拒绝说明喂回模型。
	PermitDeny
)

// Tool 是一个可被模型调用的工具。
//
// Permission 控制是否可执行;Approval=true 等价于 Permission=PermitAsk
// (仅当 Permission 仍为默认 Allow 时生效)。
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

// DefaultMaxSteps 是未设置 Runner.MaxSteps 时的默认步数上限。
const DefaultMaxSteps = 8

// TriggerType 标识一次 agent 运行(及其发出的 Event)由何种事件触发。
type TriggerType string

const (
	TriggerUser     TriggerType = "user"
	TriggerToolCall TriggerType = "tool_call"
	TriggerTransfer TriggerType = "transfer"
)

type triggerCtxKey struct{}

// WithTrigger 在 ctx 上标注触发信息,使 Event 带上 TriggerType/TriggerID。
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
	EventToken      EventType = "token"
	EventStep       EventType = "step"
	EventToolStart  EventType = "tool_start"
	EventToolResult EventType = "tool_result"
	EventSteer      EventType = "steer"
	EventPaused     EventType = "paused" // 等待 Continue;Requirements 非空
	EventFinal      EventType = "final"
	EventError      EventType = "error"
)

// Event 是 RunStream / ContinueStream 推送的一条事件。
type Event struct {
	Type         EventType
	Step         int
	Response     *llm.Response
	ToolCall     *llm.ToolCall
	Result       string
	Err          error
	RunID        string
	Requirements []Requirement

	AgentName   string
	TriggerType TriggerType
	TriggerID   string
}

// Hooks 是分层观测/拦截点。任一回调返回 error 会中止整个 Run。
//
// 生命周期层级:
//
//	Turn (整轮)
//	  BeforeTurn → [step 1..N] → AfterTurn
//
//	Step (单步 = 一次模型调用 + 其工具调用)
//	  BeforeModel → [model call / OnChunk*] → AfterModel
//	  → BeforeTool → [exec] → AfterTool (每个工具)
//
// 全部 Waterfall 语义:
//   - BeforeModel 接收 *llm.Request,可改写 Messages/System
//   - BeforeTool 接收 *llm.ToolCall,可改写 Arguments;返回 Permission 可动态拦截
//   - AfterTool 接收 *string result,可改写工具返回值
//   - OnChunk 接收 *llm.Chunk,可改写 Delta(如敏感词过滤)
type Hooks struct {
	// --- Turn 级别 ---

	// BeforeTurn 在整轮循环开始前调用。可修改 req(如注入系统 prompt、过滤消息)。
	BeforeTurn func(ctx context.Context, req *llm.Request) error
	// AfterTurn 在整轮循环结束后调用(无论 Done/Paused/Error)。不返回 error(已不可挽回)。
	AfterTurn func(ctx context.Context, outcome *RunOutcome)

	// --- Model 级别 ---

	// BeforeModel 在每步模型调用前触发。可改写 req(waterfall:修改 Messages 即改写上下文)。
	BeforeModel func(ctx context.Context, step int, req *llm.Request) error
	// AfterModel 在每步模型调用后触发。
	AfterModel func(ctx context.Context, step int, resp *llm.Response) error

	// --- Stream 级别 ---

	// OnChunk 在流式生成的每个 chunk 到达时触发。接收 *llm.Chunk 可改写 Delta(如敏感词过滤)。
	// 返回 error 中止整个 run。仅流式模式下触发;非流式 Generate 不触发。
	OnChunk func(ctx context.Context, step int, chunk *llm.Chunk) error

	// --- Tool 级别 ---

	// BeforeTool 在工具执行前触发。接收 *llm.ToolCall 可改写 Arguments(如 PII 脱敏);
	// 返回 Permission 可动态拦截(PermitDeny=拒绝, PermitAsk=暂停审批, PermitAllow=放行)。
	BeforeTool func(ctx context.Context, step int, tc *llm.ToolCall) (Permission, error)
	// AfterTool 在工具执行后触发。接收 *string 可改写工具返回值(如过滤敏感信息)。
	AfterTool func(ctx context.Context, step int, tc llm.ToolCall, result *string) error
}

// Mailbox 是 Agent 运行中的统一注入信箱,支持用户消息和系统上下文两种角色。
//
//	mb := agent.NewMailbox(8)
//	mb.Steer("请改成简短回答")          // 注入 user 消息(运行中中途插话)
//	mb.Inject("用户 VIP 等级:钻石")     // 注入一次性 system 上下文
//	mb.InjectPersistent("当前库存:低")   // 注入持久 system 上下文(每步都追加)
type Mailbox struct {
	ch   chan string // user 消息(原 Steer)
	mu   sync.Mutex
	once []string // 一次性 system 上下文
	keep []string // 持久 system 上下文
}

// NewMailbox 创建信箱。buf 控制 user 消息缓冲区大小;<=0 时用 8。
func NewMailbox(buf int) *Mailbox {
	if buf < 1 {
		buf = 8
	}
	return &Mailbox{ch: make(chan string, buf)}
}

// Steer 投入一条用户级中途插话(作为 user 消息注入对话)。
func (mb *Mailbox) Steer(msg string) bool {
	if mb == nil || msg == "" {
		return false
	}
	select {
	case mb.ch <- msg:
		return true
	default:
		return false
	}
}

// Inject 注入一次性系统上下文(下次模型调用时追加到 system prompt,用后即弃)。
func (mb *Mailbox) Inject(content string) {
	if mb == nil || content == "" {
		return
	}
	mb.mu.Lock()
	defer mb.mu.Unlock()
	mb.once = append(mb.once, content)
}

// InjectPersistent 注入持久系统上下文(每次模型调用都追加到 system prompt)。
func (mb *Mailbox) InjectPersistent(content string) {
	if mb == nil || content == "" {
		return
	}
	mb.mu.Lock()
	defer mb.mu.Unlock()
	mb.keep = append(mb.keep, content)
}

// ClearPersistent 清除所有持久注入。
func (mb *Mailbox) ClearPersistent() {
	if mb == nil {
		return
	}
	mb.mu.Lock()
	defer mb.mu.Unlock()
	mb.keep = nil
}

func (mb *Mailbox) drainSteer() []string {
	if mb == nil {
		return nil
	}
	var out []string
	for {
		select {
		case m := <-mb.ch:
			out = append(out, m)
		default:
			return out
		}
	}
}

func (mb *Mailbox) drainInject() string {
	if mb == nil {
		return ""
	}
	mb.mu.Lock()
	defer mb.mu.Unlock()

	total := len(mb.keep) + len(mb.once)
	if total == 0 {
		return ""
	}

	parts := make([]string, 0, total)
	parts = append(parts, mb.keep...)
	parts = append(parts, mb.once...)
	mb.once = nil

	var b []byte
	for i, p := range parts {
		if i > 0 {
			b = append(b, '\n')
		}
		b = append(b, p...)
	}
	return string(b)
}

// Info 是一个 Agent 的元信息。
type Info struct {
	Name        string
	Description string
	Tools       []llm.ToolDef
}

// Agent 是可被统一编排的最小契约:Run/Continue 返回 RunOutcome。
type Agent interface {
	Run(ctx context.Context, req llm.Request) RunOutcome
	Continue(ctx context.Context, runID string, resolutions []Resolution) RunOutcome
	Info() Info
}

// StreamAgent 额外支持流式事件。
type StreamAgent interface {
	Agent
	RunStream(ctx context.Context, req llm.Request) <-chan Event
	ContinueStream(ctx context.Context, runID string, resolutions []Resolution) <-chan Event
}

// ToolScope 在每步模型调用前过滤可用工具子集(per-agent scoping)。
type ToolScope interface {
	Filter(ctx context.Context, step int, tools []Tool) []Tool
}

// ToolScopeFunc 是 ToolScope 的函数适配器。
type ToolScopeFunc func(ctx context.Context, step int, tools []Tool) []Tool

func (f ToolScopeFunc) Filter(ctx context.Context, step int, tools []Tool) []Tool {
	return f(ctx, step, tools)
}

// Runner 驱动 agent 循环。PermitAsk 工具触发整轮原子暂停,经 Continue 决议后继续。
type Runner struct {
	Client   llm.Client
	Tools    []Tool
	MaxSteps int

	Name           string
	Description    string
	Planner        Planner
	ParallelTools  *bool
	OnStep         func(step int, resp *llm.Response)
	Hooks          Hooks
	Mailbox        *Mailbox // 统一注入信箱(user 插话 + system 上下文)
	RepairToolArgs bool
	Compactor      *Compactor

	// Scope 在每步模型调用前过滤可用工具子集。nil 时使用全部 Tools。
	Scope ToolScope

	// Store 持久化暂停快照;nil 时用内置 MemoryRunStore(进程内)。
	Store RunStore

	// nestedResume 保存 AgentAsTool 等冒泡暂停时的子 Continue 回调(进程内,不入 Store)。
	nestedResume sync.Map // runID → func(ctx, []Resolution) RunOutcome

	// storeOnce 保护 Store 的懒初始化:同一个 *Runner 可被 BestOfN/Parallel 等并发复用,
	// 多个 goroutine 可能同时首次调用 Run/Continue,若不加同步会在 Store 字段上产生数据竞争。
	storeOnce sync.Once
}

var _ StreamAgent = (*Runner)(nil)

// ensureStore 在首次 Run 时初始化默认 Store。并发安全(见 storeOnce 注释)。
func (r *Runner) ensureStore() {
	r.storeOnce.Do(func() {
		if r.Store == nil {
			r.Store = NewMemoryRunStore()
		}
	})
}

// Info 实现 Agent。
func (r *Runner) Info() Info {
	defs := make([]llm.ToolDef, len(r.Tools))
	for i, t := range r.Tools {
		defs[i] = t.Def
	}
	return Info{Name: r.Name, Description: r.Description, Tools: defs}
}

// Run 启动工具循环,返回 Done / Paused / Error。
func (r *Runner) Run(ctx context.Context, req llm.Request) RunOutcome {
	r.ensureStore()
	runID := newRunID()
	frame := checkpoint.FrameFrom(ctx)
	frame.RunID = runID
	if frame.AgentName == "" {
		frame.AgentName = r.Name
	}
	ctx = checkpoint.WithFrame(ctx, frame)
	return r.runLoop(ctx, runID, req, nil, 1, nil)
}

// Continue 恢复暂停的 run:先按 resolutions 执行 pending tool_calls,再继续循环。
func (r *Runner) Continue(ctx context.Context, runID string, resolutions []Resolution) RunOutcome {
	r.ensureStore()
	if runID == "" {
		return outcomeError("", nil, nil, fmt.Errorf("agent: Continue requires runID"))
	}
	snap, err := r.loadSnapshot(ctx, runID)
	if err != nil {
		return outcomeError(runID, nil, nil, err)
	}
	if snap == nil {
		return outcomeError(runID, nil, nil, fmt.Errorf("agent: unknown runID %q", runID))
	}
	if snap.Kind != "" && snap.Kind != "runner" {
		return outcomeError(runID, nil, nil, fmt.Errorf("agent: runID %q is kind %q, not runner", runID, snap.Kind))
	}

	r.appendCheckpoint(ctx, runID, checkpoint.NewEvent(checkpoint.TypeRunResumed, runID))

	// 嵌套子 pause(AgentAsTool):先 Continue 子 run,把结果当 tool result,再继续。
	if snap.ChildRunID != "" && len(snap.PendingTCs) == 0 {
		return r.continueNested(ctx, runID, snap, resolutions)
	}

	byRes := map[string]Resolution{}
	for _, res := range resolutions {
		byRes[res.ID] = res
	}
	for _, req := range snap.Requirements {
		if _, ok := byRes[req.ID]; !ok {
			return outcomeError(runID, nil, snap.Messages, fmt.Errorf("agent: missing resolution for %q", req.ID))
		}
	}

	byName := r.toolIndex()
	req := snap.Request
	msgs := cloneMessages(snap.Messages)
	step := snap.Step

	toolMsgs, fatal, nested := r.execPending(ctx, step, byName, snap.PendingTCs, snap.Requirements, byRes, nil)
	if fatal != nil {
		return outcomeError(runID, nil, msgs, fatal)
	}
	if nested != nil {
		// 执行 Allow 工具时又冒出嵌套 pause:保存进度后返回。
		msgs = append(msgs, toolMsgs...)
		return r.pauseNested(ctx, runID, req, msgs, nil, step, nested)
	}
	msgs = append(msgs, toolMsgs...)
	_ = r.Store.Delete(ctx, runID)
	return r.runLoop(ctx, runID, req, msgs, step+1, nil)
}

func (r *Runner) continueNested(ctx context.Context, runID string, snap *RunSnapshot, resolutions []Resolution) RunOutcome {
	v, ok := r.nestedResume.Load(runID)
	if !ok {
		return outcomeError(runID, nil, snap.Messages, fmt.Errorf("agent: nested resume lost for run %q (child=%s)", runID, snap.ChildRunID))
	}
	resume := v.(func(context.Context, []Resolution) RunOutcome)
	childRes := filterResolutions(resolutions, snap.ChildSource)
	childOut := resume(ctx, childRes)
	switch childOut.Status {
	case StatusPaused:
		reqs := remapRequirements(childOut.Requirements, snap.ChildSource)
		snap.Requirements = reqs
		snap.ChildRunID = childOut.RunID
		if err := r.saveCheckpoint(ctx, runID, snap); err != nil {
			return outcomeError(runID, childOut.Response, snap.Messages, err)
		}
		return outcomePaused(runID, childOut.Response, snap.Messages, reqs)
	case StatusError:
		return outcomeError(runID, childOut.Response, snap.Messages, childOut.Err)
	case StatusDone:
		r.nestedResume.Delete(runID)
		msgs := cloneMessages(snap.Messages)
		toolName := strings.TrimPrefix(snap.ChildSource, "tool:")
		tcID := nestedToolCallID(msgs, toolName)
		content := ""
		if childOut.Response != nil {
			content = childOut.Response.Content
		}
		msgs = append(msgs, llm.Message{Role: llm.Tool, ToolCallID: tcID, Content: content})
		doneEv := checkpoint.NewEvent(checkpoint.TypeAgentCompleted, runID)
		doneEv.ChildRunID = snap.ChildRunID
		doneEv.Source = snap.ChildSource
		doneEv.Result = content
		r.appendCheckpoint(ctx, runID, doneEv)
		_ = r.Store.Delete(ctx, runID)
		req := snap.Request
		return r.runLoop(ctx, runID, req, msgs, snap.Step+1, nil)
	default:
		return outcomeError(runID, childOut.Response, snap.Messages, fmt.Errorf("agent: unexpected child status %q", childOut.Status))
	}
}

func filterResolutions(resolutions []Resolution, source string) []Resolution {
	if source == "" {
		return resolutions
	}
	prefix := source + "/"
	var out []Resolution
	for _, r := range resolutions {
		// 顶层暴露的 Requirement.ID 未改;Source 仅作标注。全部转交子 Continue。
		_ = prefix
		out = append(out, r)
	}
	if len(out) == 0 {
		return resolutions
	}
	return out
}

func nestedToolCallID(msgs []llm.Message, toolName string) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != llm.Assistant {
			continue
		}
		for _, tc := range msgs[i].ToolCalls {
			if tc.Name == toolName {
				return tc.ID
			}
		}
	}
	return ""
}

// RunStream 异步跑循环并推送事件;Paused 时发 EventPaused 后关闭 channel。
func (r *Runner) RunStream(ctx context.Context, req llm.Request) <-chan Event {
	return r.streamOutcome(ctx, func(emit func(Event)) RunOutcome {
		r.ensureStore()
		runID := newRunID()
		return r.runLoop(ctx, runID, req, nil, 1, emit)
	})
}

// ContinueStream 是 Continue 的流式版。
func (r *Runner) ContinueStream(ctx context.Context, runID string, resolutions []Resolution) <-chan Event {
	return r.streamOutcome(ctx, func(emit func(Event)) RunOutcome {
		// 复用 Continue 逻辑但需要 emit——抽 runContinueWithEmit
		return r.continueWithEmit(ctx, runID, resolutions, emit)
	})
}

func (r *Runner) streamOutcome(ctx context.Context, fn func(emit func(Event)) RunOutcome) <-chan Event {
	ch := make(chan Event, 32)
	go func() {
		defer close(ch)
		tt, tid := triggerFrom(ctx)
		emit := func(e Event) {
			e.AgentName = r.Name
			e.TriggerType = tt
			e.TriggerID = tid
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

func (r *Runner) continueWithEmit(ctx context.Context, runID string, resolutions []Resolution, emit func(Event)) RunOutcome {
	r.ensureStore()
	if runID == "" {
		return outcomeError("", nil, nil, fmt.Errorf("agent: Continue requires runID"))
	}
	snap, err := r.loadSnapshot(ctx, runID)
	if err != nil {
		return outcomeError(runID, nil, nil, err)
	}
	if snap == nil {
		return outcomeError(runID, nil, nil, fmt.Errorf("agent: unknown runID %q", runID))
	}
	if snap.ChildRunID != "" && len(snap.PendingTCs) == 0 {
		return r.continueNested(ctx, runID, snap, resolutions)
	}
	r.appendCheckpoint(ctx, runID, checkpoint.NewEvent(checkpoint.TypeRunResumed, runID))
	byRes := map[string]Resolution{}
	for _, res := range resolutions {
		byRes[res.ID] = res
	}
	for _, req := range snap.Requirements {
		if _, ok := byRes[req.ID]; !ok {
			return outcomeError(runID, nil, snap.Messages, fmt.Errorf("agent: missing resolution for %q", req.ID))
		}
	}
	byName := r.toolIndex()
	req := snap.Request
	msgs := cloneMessages(snap.Messages)
	step := snap.Step
	emitUI := func(e Event) { r.emitEvent(ctx, runID, emit, e) }
	toolMsgs, fatal, nested := r.execPending(ctx, step, byName, snap.PendingTCs, snap.Requirements, byRes, emitUI)
	if fatal != nil {
		return outcomeError(runID, nil, msgs, fatal)
	}
	if nested != nil {
		msgs = append(msgs, toolMsgs...)
		return r.pauseNested(ctx, runID, req, msgs, nil, step, nested)
	}
	msgs = append(msgs, toolMsgs...)
	_ = r.Store.Delete(ctx, runID)
	return r.runLoop(ctx, runID, req, msgs, step+1, emit)
}

func (r *Runner) toolIndex() map[string]Tool {
	byName := make(map[string]Tool, len(r.Tools))
	for _, t := range r.Tools {
		byName[t.Def.Name] = t
	}
	return byName
}

func (r *Runner) runLoop(ctx context.Context, runID string, req llm.Request, msgs []llm.Message, startStep int, emit func(Event)) RunOutcome {
	maxSteps := r.MaxSteps
	if maxSteps <= 0 {
		maxSteps = DefaultMaxSteps
	}

	// BeforeTurn: 整轮开始前的拦截点(仅首次进入,Continue 不重复触发)。
	if r.Hooks.BeforeTurn != nil && startStep == 1 && msgs == nil {
		if err := r.Hooks.BeforeTurn(ctx, &req); err != nil {
			out := outcomeError(runID, nil, nil, err)
			r.afterTurn(ctx, &out)
			return out
		}
	}

	// Scope: 按上下文过滤可用工具子集。
	activeTools := r.Tools
	if r.Scope != nil {
		activeTools = r.Scope.Filter(ctx, startStep, r.Tools)
	}
	defs := make([]llm.ToolDef, len(activeTools))
	byName := make(map[string]Tool, len(activeTools))
	for i, t := range activeTools {
		defs[i] = t.Def
		byName[t.Def.Name] = t
	}
	req.Tools = defs

	if r.Planner != nil && startStep == 1 && msgs == nil {
		if instr := r.Planner.BuildPlanningInstruction(&req); instr != "" {
			if req.System != "" {
				req.System += "\n\n"
			}
			req.System += instr
		}
	}

	if msgs == nil {
		msgs = make([]llm.Message, len(req.Messages))
		copy(msgs, req.Messages)
	}

	if startStep == 1 {
		r.appendCheckpoint(ctx, runID, checkpoint.NewEvent(checkpoint.TypeRunStarted, runID).WithStep(startStep))
		for _, m := range req.Messages {
			if m.Role != llm.User {
				continue
			}
			ev := checkpoint.NewEvent(checkpoint.TypeUserMessage, runID).WithStep(0)
			msg := m
			ev.Message = &msg
			r.appendCheckpoint(ctx, runID, ev)
		}
	}

	var last *llm.Response
	for step := startStep; step <= maxSteps; step++ {
		if err := ctx.Err(); err != nil {
			out := outcomeError(runID, last, msgs, err)
			r.afterTurn(ctx, &out)
			return out
		}

		// Scope 每步重新评估(工具可用性可能随上下文变化)。
		if r.Scope != nil {
			activeTools = r.Scope.Filter(ctx, step, r.Tools)
			defs = make([]llm.ToolDef, len(activeTools))
			byName = make(map[string]Tool, len(activeTools))
			for i, t := range activeTools {
				defs[i] = t.Def
				byName[t.Def.Name] = t
			}
			req.Tools = defs
		}

		// Mailbox: 用户插话 + 系统上下文注入。
		for _, m := range r.Mailbox.drainSteer() {
			msgs = append(msgs, llm.Message{Role: llm.User, Content: m})
			r.emitEvent(ctx, runID, emit, Event{Type: EventSteer, Step: step, Result: m, RunID: runID})
		}
		if extra := r.Mailbox.drainInject(); extra != "" {
			if req.System != "" {
				req.System += "\n\n"
			}
			req.System += extra
		}

		req.Messages = msgs
		if r.Hooks.BeforeModel != nil {
			if err := r.Hooks.BeforeModel(ctx, step, &req); err != nil {
				out := outcomeError(runID, last, msgs, err)
				r.afterTurn(ctx, &out)
				return out
			}
			msgs = req.Messages
		}
		if r.Compactor != nil {
			req.Messages = r.Compactor.Project(req.Messages)
		}

		var modelEmit func(Event)
		if emit != nil || r.Hooks.OnChunk != nil {
			modelEmit = func(e Event) { r.emitEvent(ctx, runID, emit, e) }
		}
		resp, err := r.callModel(ctx, req, step, modelEmit)
		if err != nil {
			out := outcomeError(runID, last, msgs, err)
			r.afterTurn(ctx, &out)
			return out
		}
		if r.Planner != nil {
			resp = r.Planner.ProcessPlanningResponse(step, resp)
		}
		last = resp
		if r.Hooks.AfterModel != nil {
			if err := r.Hooks.AfterModel(ctx, step, resp); err != nil {
				out := outcomeError(runID, last, msgs, err)
				r.afterTurn(ctx, &out)
				return out
			}
		}
		if r.OnStep != nil {
			r.OnStep(step, resp)
		}
		r.emitEvent(ctx, runID, emit, Event{Type: EventStep, Step: step, Response: resp, RunID: runID})

		if len(resp.ToolCalls) == 0 {
			_ = r.Store.Delete(ctx, runID)
			r.appendCheckpoint(ctx, runID, checkpoint.NewEvent(checkpoint.TypeRunCompleted, runID).WithStep(step))
			out := outcomeDone(runID, resp, msgs)
			r.afterTurn(ctx, &out)
			return out
		}

		msgs = append(msgs, llm.Message{Role: llm.Assistant, Content: resp.Content, ToolCalls: resp.ToolCalls})

		// 原子暂停:任一轮含 PermitAsk → 整轮不执行任何工具。
		if reqs := r.askRequirements(byName, resp.ToolCalls); len(reqs) > 0 {
			snap := &RunSnapshot{
				Kind:         "runner",
				Request:      req,
				Messages:     cloneMessages(msgs),
				PendingTCs:   append([]llm.ToolCall{}, resp.ToolCalls...),
				Requirements: reqs,
				Step:         step,
			}
			r.checkpointPaused(ctx, runID, nil, step, resp, reqs)
			if err := r.saveCheckpoint(ctx, runID, snap); err != nil {
				out := outcomeError(runID, last, msgs, err)
				r.afterTurn(ctx, &out)
				return out
			}
			out := outcomePaused(runID, resp, msgs, reqs)
			r.afterTurn(ctx, &out)
			return out
		}

		emitUI := func(e Event) { r.emitEvent(ctx, runID, emit, e) }
		toolMsgs, fatal, nested := r.runTools(ctx, step, byName, resp.ToolCalls, emitUI)
		if fatal != nil {
			var np *NestedPauseError
			if errors.As(fatal, &np) {
				msgs = append(msgs, toolMsgs...)
				return r.pauseNested(ctx, runID, req, msgs, resp, step, np)
			}
			out := outcomeError(runID, last, msgs, fatal)
			r.afterTurn(ctx, &out)
			return out
		}
		if nested != nil {
			msgs = append(msgs, toolMsgs...)
			return r.pauseNested(ctx, runID, req, msgs, resp, step, nested)
		}
		msgs = append(msgs, toolMsgs...)
	}
	out := outcomeError(runID, last, msgs, ErrMaxSteps)
	r.appendCheckpoint(ctx, runID, checkpoint.NewEvent(checkpoint.TypeRunError, runID).WithStep(maxSteps))
	r.afterTurn(ctx, &out)
	return out
}

func (r *Runner) afterTurn(ctx context.Context, out *RunOutcome) {
	if r.Hooks.AfterTurn != nil {
		r.Hooks.AfterTurn(ctx, out)
	}
}

func (r *Runner) pauseNested(ctx context.Context, runID string, req llm.Request, msgs []llm.Message, resp *llm.Response, step int, np *NestedPauseError) RunOutcome {
	reqs := remapRequirements(np.Child.Requirements, np.Source)
	snap := &RunSnapshot{
		Kind:         "runner",
		Request:      req,
		Messages:     cloneMessages(msgs),
		Step:         step,
		ChildRunID:   np.Child.RunID,
		ChildSource:  np.Source,
		Requirements: reqs,
	}
	if np.Resume != nil {
		r.nestedResume.Store(runID, np.Resume)
	}
	spawnEv := checkpoint.NewEvent(checkpoint.TypeAgentSpawned, runID).WithStep(step)
	spawnEv.ChildRunID = np.Child.RunID
	spawnEv.Source = np.Source
	r.appendCheckpoint(ctx, runID, spawnEv)
	r.checkpointPaused(ctx, runID, nil, step, resp, reqs)
	if err := r.saveCheckpoint(ctx, runID, snap); err != nil {
		return outcomeError(runID, resp, msgs, err)
	}
	return outcomePaused(runID, resp, msgs, reqs)
}

func remapRequirements(reqs []Requirement, source string) []Requirement {
	out := make([]Requirement, len(reqs))
	for i, rq := range reqs {
		out[i] = rq
		if rq.Source == "" {
			out[i].Source = source
		} else {
			out[i].Source = source + "/" + rq.Source
		}
	}
	return out
}

func (r *Runner) askRequirements(byName map[string]Tool, tcs []llm.ToolCall) []Requirement {
	var reqs []Requirement
	for i, tc := range tcs {
		t, ok := byName[tc.Name]
		perm := PermitAllow
		if ok {
			perm = t.effectivePerm()
		}
		if perm == PermitAsk {
			id := tc.ID
			if id == "" {
				id = fmt.Sprintf("ask-%d", i)
			}
			reqs = append(reqs, Requirement{ID: id, ToolCall: tc})
		}
	}
	return reqs
}

func (r *Runner) callModel(ctx context.Context, req llm.Request, step int, emit func(Event)) (*llm.Response, error) {
	if emit == nil && r.Hooks.OnChunk == nil {
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
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if c.Err != nil {
			return nil, c.Err
		}
		// OnChunk: 流式拦截(可改写 Delta,如敏感词过滤)。
		if r.Hooks.OnChunk != nil {
			if err := r.Hooks.OnChunk(ctx, step, &c); err != nil {
				return nil, err
			}
		}
		if c.Delta != "" {
			content.WriteString(c.Delta)
			if emit != nil {
				emit(Event{Type: EventToken, Step: step, Result: c.Delta})
			}
		}
		if len(c.ToolCalls) > 0 {
			toolCalls = c.ToolCalls
		}
		if c.Usage != nil {
			usage = *c.Usage
		}
	}
	resp := &llm.Response{Content: content.String(), ToolCalls: toolCalls, Usage: usage, Model: req.Model}
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

func (r *Runner) runTools(ctx context.Context, step int, byName map[string]Tool, tcs []llm.ToolCall, emit func(Event)) ([]llm.Message, error, *NestedPauseError) {
	return r.execToolCalls(ctx, step, byName, tcs, nil, nil, emit)
}

// execPending 在 Continue 时执行整轮 pending:Ask 用 resolutions,其余走正常 dispatch。
func (r *Runner) execPending(ctx context.Context, step int, byName map[string]Tool, tcs []llm.ToolCall, reqs []Requirement, byRes map[string]Resolution, emit func(Event)) ([]llm.Message, error, *NestedPauseError) {
	ask := map[string]Requirement{}
	for _, rq := range reqs {
		ask[rq.ID] = rq
	}
	return r.execToolCalls(ctx, step, byName, tcs, ask, byRes, emit)
}

func (r *Runner) execToolCalls(ctx context.Context, step int, byName map[string]Tool, tcs []llm.ToolCall, ask map[string]Requirement, byRes map[string]Resolution, emit func(Event)) ([]llm.Message, error, *NestedPauseError) {
	if !r.parallelEnabled(len(tcs)) {
		var msgs []llm.Message
		for i, tc := range tcs {
			if err := ctx.Err(); err != nil {
				return msgs, err, nil
			}
			out := r.execOne(ctx, step, byName, tc, ask, byRes, i, emit)
			if out.fatal != nil {
				var np *NestedPauseError
				if errors.As(out.fatal, &np) {
					return msgs, nil, np
				}
				return msgs, out.fatal, nil
			}
			msgs = append(msgs, llm.Message{Role: llm.Tool, ToolCallID: tc.ID, Content: out.result})
		}
		return msgs, nil, nil
	}

	outs := make([]toolOutcome, len(tcs))
	var wg sync.WaitGroup
	for i, tc := range tcs {
		wg.Add(1)
		go func(i int, tc llm.ToolCall) {
			defer wg.Done()
			if err := ctx.Err(); err != nil {
				outs[i] = toolOutcome{tc: tc, fatal: err}
				return
			}
			outs[i] = r.execOne(ctx, step, byName, tc, ask, byRes, i, emit)
		}(i, tc)
	}
	wg.Wait()

	msgs := make([]llm.Message, 0, len(tcs))
	var nested *NestedPauseError
	for i, out := range outs {
		if out.fatal != nil {
			var np *NestedPauseError
			if errors.As(out.fatal, &np) {
				if nested == nil {
					nested = np
				}
				continue
			}
			return msgs, out.fatal, nil
		}
		msgs = append(msgs, llm.Message{Role: llm.Tool, ToolCallID: tcs[i].ID, Content: out.result})
	}
	if nested != nil {
		return msgs, nil, nested
	}
	return msgs, nil, nil
}

func (r *Runner) execOne(ctx context.Context, step int, byName map[string]Tool, tc llm.ToolCall, ask map[string]Requirement, byRes map[string]Resolution, idx int, emit func(Event)) toolOutcome {
	// BeforeTool: 动态 gate + 参数改写(waterfall)。
	if r.Hooks.BeforeTool != nil {
		perm, err := r.Hooks.BeforeTool(ctx, step, &tc)
		if err != nil {
			return toolOutcome{tc: tc, fatal: err}
		}
		switch perm {
		case PermitDeny:
			result := fmt.Sprintf("工具 %q 被策略拒绝(deny)", tc.Name)
			if emit != nil {
				emit(Event{Type: EventToolResult, Step: step, ToolCall: &tc, Result: result})
			}
			return toolOutcome{tc: tc, result: result}
		case PermitAsk:
			result := fmt.Sprintf("工具 %q 需要审批(ask)", tc.Name)
			return toolOutcome{tc: tc, result: result}
		}
	}
	if emit != nil {
		tc := tc
		emit(Event{Type: EventToolStart, Step: step, ToolCall: &tc})
	}
	result, fatal := r.dispatch(ctx, byName, tc, ask, byRes, idx)
	if fatal != nil {
		return toolOutcome{tc: tc, result: result, fatal: fatal}
	}
	if emit != nil {
		tc := tc
		emit(Event{Type: EventToolResult, Step: step, ToolCall: &tc, Result: result})
	}
	// AfterTool: 可改写 result(waterfall)。
	if r.Hooks.AfterTool != nil {
		if err := r.Hooks.AfterTool(ctx, step, tc, &result); err != nil {
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

func (r *Runner) dispatch(ctx context.Context, byName map[string]Tool, tc llm.ToolCall, ask map[string]Requirement, byRes map[string]Resolution, idx int) (result string, fatal error) {
	t, ok := byName[tc.Name]
	if !ok {
		return fmt.Sprintf("error: unknown tool %q", tc.Name), nil
	}
	perm := t.effectivePerm()
	switch perm {
	case PermitDeny:
		return fmt.Sprintf("工具 %q 被策略拒绝(deny),不可调用", tc.Name), nil
	case PermitAsk:
		id := tc.ID
		if id == "" {
			id = fmt.Sprintf("ask-%d", idx)
		}
		if byRes == nil {
			// 不应在未暂停路径执行到 Ask(原子暂停已拦截);防御性拒绝。
			return fmt.Sprintf("工具 %q 需要审批但未提供决议", tc.Name), nil
		}
		res, ok := byRes[id]
		if !ok {
			return "", fmt.Errorf("agent: missing resolution for %q", id)
		}
		if !res.Approved {
			msg := fmt.Sprintf("工具 %q 的调用被拒绝", tc.Name)
			if res.Reason != "" {
				msg += ": " + res.Reason
			}
			return msg, nil
		}
	}
	args := tc.Arguments
	if r.RepairToolArgs && len(args) > 0 && !json.Valid(args) {
		if fixed, ok := RepairJSON(args); ok {
			args = fixed
		}
	}
	out, err := t.Call(ctx, args)
	if err != nil {
		var np *NestedPauseError
		if errors.As(err, &np) {
			return "", np
		}
		return "error: " + err.Error(), nil
	}
	return out, nil
}

// Bool 返回 *bool,便于设置 ParallelTools。
func Bool(v bool) *bool { return &v }
