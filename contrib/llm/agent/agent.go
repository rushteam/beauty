// Package agent 是 beauty 在 contrib/llm 之上的薄 agent 循环:模型↔工具循环,直到终态、
// 步数上限或 PermitAsk 原子暂停。Run/Continue 返回 iter.Seq2[Event, error];CollectOutcome
// 可收束为 RunOutcome。审批不进 Runner 核心(产品路径显式 Continue;阻塞审批用外置 SyncHITL)。
//
// Runner / Chain / Team / Parallel / GroupChat / BestOfN / VerifyLoop 实现同一 Agent 契约,可互相嵌套。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent/checkpoint"
	"github.com/rushteam/beauty/contrib/llm/agent/compaction"
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

// Tool 是一个可被模型调用的工具。Permission 控制是否可执行。
type Tool struct {
	Def        llm.ToolDef
	Call       func(ctx context.Context, args json.RawMessage) (string, error)
	Permission Permission
}

// Func 是构造 Tool 的便捷函数(默认 PermitAllow)。
func Func(name, description string, parameters json.RawMessage, call func(context.Context, json.RawMessage) (string, error)) Tool {
	return Tool{Def: llm.ToolDef{Name: name, Description: description, Parameters: parameters}, Call: call}
}

// DefaultMaxSteps 是未设置 Runner.MaxSteps 时的默认步数上限。
const DefaultMaxSteps = 8

// DefaultMaxConsecutiveErrors 是连续工具全失败轮数的默认熔断阈值。
const DefaultMaxConsecutiveErrors = 3

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

// EventType 标识 Run 事件流中的事件种类。
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

// Event 是 Run / Continue 产出的一条事件。
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

// Agent 是可被统一编排的最小契约:Run/Continue 返回事件流。
type Agent interface {
	Run(ctx context.Context, req llm.Request, opts ...Option) iter.Seq2[Event, error]
	Continue(ctx context.Context, runID string, resolutions []Resolution, opts ...Option) iter.Seq2[Event, error]
	Info() Info
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

	// MaxConsecutiveErrors 连续工具全失败轮数达到此值时熔断(默认 3)。
	// 防止工具持续报错时无意义消耗步数。
	MaxConsecutiveErrors int

	Name           string
	Description    string
	Planner        Planner
	ParallelTools  *bool
	Hooks          Hooks
	Mailbox        *Mailbox // 统一注入信箱(user 插话 + system 上下文)
	RepairToolArgs bool
	Compaction     compaction.Strategy

	// Scope 在每步模型调用前过滤可用工具子集。nil 时使用全部 Tools。
	Scope ToolScope

	// Store 持久化暂停快照;nil 时用内置 MemoryRunStore(进程内)。
	Store RunStore

	// ---- 新增:双层中间件 + History/Context Provider ----

	// Middlewares 是 Agent 级中间件链。在 History/Context 注入之后、核心循环前后生效。
	// 与 llm.Client 装饰器(Provider 级)分层互补。
	Middlewares []AgentMiddleware

	// HistoryProv 在运行前加载历史消息,成功后持久化。nil 时不管理历史。
	HistoryProv HistoryProvider

	// ContextProvs 在运行前注入上下文消息和临时工具。nil 时不注入额外上下文。
	ContextProvs []ContextProvider

	// SessionID 用于 HistoryProvider 的 session 标识。空串时回退到 Name。
	SessionID string

	// ApprovalRules 持久化的 standing approval 规则。匹配的 PermitAsk 工具自动放行。
	// nil 时不启用(每次都要求审批)。
	ApprovalRules *ApprovalStore

	// Policy 工具准入策略(Allow/Deny/Ask + Mode)。与 Tool.Permission 取更严者;
	// Ask 接到现有 HITL(Pause/Continue)。Deny 工具不会广告给模型。
	Policy *ToolPolicy

	// Recovery 模型调用失败时的同 run 恢复(prompt_too_long / max_output_tokens)。
	// nil 时用 DefaultRecovery。
	Recovery *Recovery

	// nestedResume 保存 AgentAsTool 等冒泡暂停时的子 Continue 回调(进程内,不入 Store)。
	nestedResume sync.Map // runID → func(ctx, []Resolution, ...Option) iter.Seq2[Event, error]

	// storeOnce 保护 Store 的懒初始化:同一个 *Runner 可被 BestOfN/Parallel 等并发复用,
	// 多个 goroutine 可能同时首次调用 Run/Continue,若不加同步会在 Store 字段上产生数据竞争。
	storeOnce sync.Once
}

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

func (r *Runner) continueNested(ctx context.Context, runID string, snap *RunSnapshot, resolutions []Resolution) RunOutcome {
	v, ok := r.nestedResume.Load(runID)
	if !ok {
		return outcomeError(runID, nil, snap.Messages, fmt.Errorf("agent: nested resume lost for run %q (child=%s)", runID, snap.ChildRunID))
	}
	resume := v.(func(context.Context, []Resolution, ...Option) iter.Seq2[Event, error])
	childRes := filterResolutions(resolutions, snap.ChildSource)
	childOut := CollectOutcome(resume(ctx, childRes))
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
		maxSteps := r.MaxSteps
		if maxSteps <= 0 {
			maxSteps = DefaultMaxSteps
		}
		return r.runLoop(ctx, runID, req, msgs, snap.Step+1, nil, r.Tools, maxSteps)
	default:
		return outcomeError(runID, childOut.Response, snap.Messages, fmt.Errorf("agent: unexpected child status %q", childOut.Status))
	}
}

func filterResolutions(resolutions []Resolution, _ string) []Resolution {
	// 顶层暴露的 Requirement.ID 未改;Source 仅作标注。全部转交子 Continue。
	return resolutions
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
			perm = r.effectivePerm(t, tc)
		} else if r.Policy != nil {
			perm = r.Policy.Decide(tc)
		}
		if perm == PermitAsk {
			// Standing rules 自动放行:匹配时跳过审批
			if r.ApprovalRules != nil && r.ApprovalRules.IsApproved(tc) {
				continue
			}
			id := tc.ID
			if id == "" {
				id = fmt.Sprintf("ask-%d", i)
			}
			reqs = append(reqs, Requirement{ID: id, ToolCall: tc})
		}
	}
	return reqs
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

func (r *Runner) effectivePerm(t Tool, tc llm.ToolCall) Permission {
	perm := t.Permission
	if r.Policy != nil {
		perm = stricterPerm(perm, r.Policy.Decide(tc))
	}
	return perm
}

func (r *Runner) dispatch(ctx context.Context, byName map[string]Tool, tc llm.ToolCall, _ map[string]Requirement, byRes map[string]Resolution, idx int) (result string, fatal error) {
	t, ok := byName[tc.Name]
	if !ok {
		return fmt.Sprintf("error: unknown tool %q", tc.Name), nil
	}
	perm := r.effectivePerm(t, tc)
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
