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
type Hooks struct {
	BeforeModel func(ctx context.Context, step int, req *llm.Request) error
	AfterModel  func(ctx context.Context, step int, resp *llm.Response) error
	BeforeTool  func(ctx context.Context, step int, tc llm.ToolCall) error
	AfterTool   func(ctx context.Context, step int, tc llm.ToolCall, result string) error
}

// Steer 是中途插话信箱。
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

// Enqueue 投入一条用户插话。
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

// Runner 驱动 agent 循环。PermitAsk 工具触发整轮原子暂停,经 Continue 决议后继续。
type Runner struct {
	Client   llm.Client
	Tools    []Tool
	MaxSteps int

	Name        string
	Description string
	Planner     Planner
	ParallelTools *bool
	OnStep      func(step int, resp *llm.Response)
	Hooks       Hooks
	Steer       *Steer
	RepairToolArgs bool
	Compactor   *Compactor

	// Store 持久化暂停快照;nil 时用内置 MemoryRunStore(进程内)。
	Store RunStore

	// nestedResume 保存 AgentAsTool 等冒泡暂停时的子 Continue 回调(进程内,不入 Store)。
	nestedResume sync.Map // runID → func(ctx, []Resolution) RunOutcome
}

var _ StreamAgent = (*Runner)(nil)

// ensureStore 在首次 Run 时初始化默认 Store。
func (r *Runner) ensureStore() {
	if r.Store == nil {
		r.Store = NewMemoryRunStore()
	}
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
	return r.runLoop(ctx, runID, req, nil, 1, nil)
}

// Continue 恢复暂停的 run:先按 resolutions 执行 pending tool_calls,再继续循环。
func (r *Runner) Continue(ctx context.Context, runID string, resolutions []Resolution) RunOutcome {
	r.ensureStore()
	if runID == "" {
		return outcomeError("", nil, nil, fmt.Errorf("agent: Continue requires runID"))
	}
	snap, err := r.Store.Load(ctx, runID)
	if err != nil {
		return outcomeError(runID, nil, nil, err)
	}
	if snap == nil {
		return outcomeError(runID, nil, nil, fmt.Errorf("agent: unknown runID %q", runID))
	}
	if snap.Kind != "" && snap.Kind != "runner" {
		return outcomeError(runID, nil, nil, fmt.Errorf("agent: runID %q is kind %q, not runner", runID, snap.Kind))
	}

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
		if err := r.Store.Save(ctx, runID, snap); err != nil {
			return outcomeError(runID, childOut.Response, snap.Messages, err)
		}
		return outcomePaused(runID, childOut.Response, snap.Messages, reqs)
	case StatusError:
		return outcomeError(runID, childOut.Response, snap.Messages, childOut.Err)
	case StatusDone:
		// 子完成:把终态文本当作工具结果写回,清除嵌套,继续父循环。
		r.nestedResume.Delete(runID)
		msgs := cloneMessages(snap.Messages)
		// 找最后一条 assistant tool_calls,为 ChildSource 对应工具补 result。
		toolName := strings.TrimPrefix(snap.ChildSource, "tool:")
		tcID := nestedToolCallID(msgs, toolName)
		content := ""
		if childOut.Response != nil {
			content = childOut.Response.Content
		}
		msgs = append(msgs, llm.Message{Role: llm.Tool, ToolCallID: tcID, Content: content})
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
	snap, err := r.Store.Load(ctx, runID)
	if err != nil {
		return outcomeError(runID, nil, nil, err)
	}
	if snap == nil {
		return outcomeError(runID, nil, nil, fmt.Errorf("agent: unknown runID %q", runID))
	}
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
	toolMsgs, fatal, nested := r.execPending(ctx, step, byName, snap.PendingTCs, snap.Requirements, byRes, emit)
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

	defs := make([]llm.ToolDef, len(r.Tools))
	byName := r.toolIndex()
	for i, t := range r.Tools {
		defs[i] = t.Def
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

	var last *llm.Response
	for step := startStep; step <= maxSteps; step++ {
		if err := ctx.Err(); err != nil {
			return outcomeError(runID, last, msgs, err)
		}

		for _, m := range r.Steer.drain() {
			msgs = append(msgs, llm.Message{Role: llm.User, Content: m})
			if emit != nil {
				emit(Event{Type: EventSteer, Step: step, Result: m, RunID: runID})
			}
		}

		req.Messages = msgs
		if r.Hooks.BeforeModel != nil {
			if err := r.Hooks.BeforeModel(ctx, step, &req); err != nil {
				return outcomeError(runID, last, msgs, err)
			}
			msgs = req.Messages
		}
		if r.Compactor != nil {
			req.Messages = r.Compactor.Project(req.Messages)
		}

		resp, err := r.callModel(ctx, req, step, emit)
		if err != nil {
			return outcomeError(runID, last, msgs, err)
		}
		if r.Planner != nil {
			resp = r.Planner.ProcessPlanningResponse(step, resp)
		}
		last = resp
		if r.Hooks.AfterModel != nil {
			if err := r.Hooks.AfterModel(ctx, step, resp); err != nil {
				return outcomeError(runID, last, msgs, err)
			}
		}
		if r.OnStep != nil {
			r.OnStep(step, resp)
		}
		if emit != nil {
			emit(Event{Type: EventStep, Step: step, Response: resp, RunID: runID})
		}

		if len(resp.ToolCalls) == 0 {
			_ = r.Store.Delete(ctx, runID)
			return outcomeDone(runID, resp, msgs)
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
			if err := r.Store.Save(ctx, runID, snap); err != nil {
				return outcomeError(runID, last, msgs, err)
			}
			return outcomePaused(runID, resp, msgs, reqs)
		}

		toolMsgs, fatal, nested := r.runTools(ctx, step, byName, resp.ToolCalls, emit)
		if fatal != nil {
			var np *NestedPauseError
			if errors.As(fatal, &np) {
				msgs = append(msgs, toolMsgs...)
				return r.pauseNested(ctx, runID, req, msgs, resp, step, np)
			}
			return outcomeError(runID, last, msgs, fatal)
		}
		if nested != nil {
			msgs = append(msgs, toolMsgs...)
			return r.pauseNested(ctx, runID, req, msgs, resp, step, nested)
		}
		msgs = append(msgs, toolMsgs...)
	}
	return outcomeError(runID, last, msgs, ErrMaxSteps)
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
	if err := r.Store.Save(ctx, runID, snap); err != nil {
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
	if r.Hooks.BeforeTool != nil {
		if err := r.Hooks.BeforeTool(ctx, step, tc); err != nil {
			return toolOutcome{tc: tc, fatal: err}
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
