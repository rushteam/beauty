package agent

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent/checkpoint"
)

var _ Agent = (*Runner)(nil)

// Run 启动工具循环,返回事件流;终态为 EventFinal / EventPaused / EventError。
func (r *Runner) Run(ctx context.Context, req llm.Request, opts ...Option) iter.Seq2[Event, error] {
	core := r.coreRunFunc()
	fn := core
	for _, mw := range r.Middlewares {
		fn = mw(fn)
	}
	return fn(ctx, req, opts...)
}

// Continue 恢复暂停的 run,返回事件流。
func (r *Runner) Continue(ctx context.Context, runID string, resolutions []Resolution, opts ...Option) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		r.ensureStore()

		maxSteps := r.MaxSteps
		if maxSteps <= 0 {
			maxSteps = DefaultMaxSteps
		}
		extraTools := []Tool(nil)
		reqCopy := reqFromContinueOpts(opts)
		applyOptions(&reqCopy, &extraTools, &maxSteps, opts)
		tools := r.Tools
		if len(extraTools) > 0 {
			tools = append(append([]Tool(nil), r.Tools...), extraTools...)
		}

		tt, tid := triggerFrom(ctx)
		emit := func(e Event) {
			e.AgentName = r.Name
			e.TriggerType = tt
			e.TriggerID = tid
			yield(e, nil)
		}

		out := r.continueLoop(ctx, runID, resolutions, emit, tools, maxSteps)
		switch out.Status {
		case StatusDone:
			yield(Event{Type: EventFinal, Response: out.Response, RunID: out.RunID, AgentName: r.Name, TriggerType: tt, TriggerID: tid}, nil)
		case StatusPaused:
			yield(Event{Type: EventPaused, Response: out.Response, RunID: out.RunID, Requirements: out.Requirements, AgentName: r.Name, TriggerType: tt, TriggerID: tid}, nil)
		default:
			yield(Event{Type: EventError, Response: out.Response, RunID: out.RunID, Err: out.Err, AgentName: r.Name, TriggerType: tt, TriggerID: tid}, out.Err)
		}
	}
}

func reqFromContinueOpts(opts []Option) llm.Request {
	var req llm.Request
	applyOptions(&req, nil, new(int), opts)
	return req
}

func (r *Runner) coreRunFunc() AgentRunFunc {
	return func(ctx context.Context, req llm.Request, opts ...Option) iter.Seq2[Event, error] {
		return func(yield func(Event, error) bool) {
			r.ensureStore()

			maxSteps := r.MaxSteps
			if maxSteps <= 0 {
				maxSteps = DefaultMaxSteps
			}
			extraTools := []Tool(nil)
			applyOptions(&req, &extraTools, &maxSteps, opts)

			tools := r.Tools
			if len(extraTools) > 0 {
				tools = append(append([]Tool(nil), r.Tools...), extraTools...)
			}

			if r.HistoryProv != nil {
				msgs, sysExtra, err := r.HistoryProv.Invoking(ctx, r.sessionID(ctx))
				if err != nil {
					yield(Event{}, err)
					return
				}
				if len(msgs) > 0 {
					marked := llm.MarkSource(msgs, llm.SourceHistory)
					req.Messages = append(marked, req.Messages...)
				}
				if sysExtra != "" {
					req.System = joinSys(req.System, sysExtra)
				}
			}

			for _, cp := range r.ContextProvs {
				msgs, ctxTools, err := cp.Invoking(ctx, &req)
				if err != nil {
					yield(Event{}, err)
					return
				}
				if len(msgs) > 0 {
					marked := llm.MarkSource(msgs, llm.SourceContext)
					req.Messages = append(req.Messages, marked...)
				}
				if len(ctxTools) > 0 {
					tools = append(tools, ctxTools...)
				}
			}

			runID := newRunID()
			frame := checkpoint.FrameFrom(ctx)
			frame.RunID = runID
			if frame.AgentName == "" {
				frame.AgentName = r.Name
			}
			ctx = checkpoint.WithFrame(ctx, frame)

			tt, tid := triggerFrom(ctx)
			emit := func(e Event) {
				e.AgentName = r.Name
				e.TriggerType = tt
				e.TriggerID = tid
				yield(e, nil)
			}

			outcome := r.runLoop(ctx, runID, req, nil, 1, emit, tools, maxSteps)

			for _, cp := range r.ContextProvs {
				_ = cp.Invoked(ctx, &outcome)
			}
			if r.HistoryProv != nil && outcome.Status == StatusDone {
				persistable := llm.ExcludeSource(outcome.Messages, llm.SourceHistory, llm.SourceContext, llm.SourceMiddleware)
				_ = r.HistoryProv.Invoked(ctx, r.sessionID(ctx), persistable)
			}

			switch outcome.Status {
			case StatusDone:
				yield(Event{Type: EventFinal, Response: outcome.Response, RunID: outcome.RunID, AgentName: r.Name, TriggerType: tt, TriggerID: tid}, nil)
			case StatusPaused:
				yield(Event{Type: EventPaused, Response: outcome.Response, RunID: outcome.RunID, Requirements: outcome.Requirements, AgentName: r.Name, TriggerType: tt, TriggerID: tid}, nil)
			default:
				yield(Event{Type: EventError, Response: outcome.Response, RunID: outcome.RunID, Err: outcome.Err, AgentName: r.Name, TriggerType: tt, TriggerID: tid}, outcome.Err)
			}
		}
	}
}

func (r *Runner) continueLoop(ctx context.Context, runID string, resolutions []Resolution, emit func(Event), tools []Tool, maxSteps int) RunOutcome {
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

	byName := r.toolIndexFrom(tools)
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
	return r.runLoop(ctx, runID, req, msgs, step+1, emit, tools, maxSteps)
}

func (r *Runner) runLoop(ctx context.Context, runID string, req llm.Request, msgs []llm.Message, startStep int, emit func(Event), tools []Tool, maxSteps int) RunOutcome {
	if r.Hooks.BeforeTurn != nil && startStep == 1 && msgs == nil {
		if err := r.Hooks.BeforeTurn(ctx, &req); err != nil {
			out := outcomeError(runID, nil, nil, err)
			r.afterTurn(ctx, &out)
			return out
		}
	}

	activeTools := tools
	if r.Scope != nil {
		activeTools = r.Scope.Filter(ctx, startStep, tools)
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
			req.System = joinSys(req.System, instr)
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

		if r.Scope != nil {
			activeTools = r.Scope.Filter(ctx, step, tools)
			defs = make([]llm.ToolDef, len(activeTools))
			byName = make(map[string]Tool, len(activeTools))
			for i, t := range activeTools {
				defs[i] = t.Def
				byName[t.Def.Name] = t
			}
			req.Tools = defs
		}

		for _, m := range r.Mailbox.drainSteer() {
			msgs = append(msgs, llm.Message{Role: llm.User, Content: m, Source: llm.SourceUser})
			r.emitEvent(ctx, runID, emit, Event{Type: EventSteer, Step: step, Result: m, RunID: runID})
		}
		if extra := r.Mailbox.drainInject(); extra != "" {
			req.System = joinSys(req.System, extra)
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
		if r.Compaction != nil {
			compact, err := r.Compaction.Compact(ctx, req.Messages)
			if err != nil {
				out := outcomeError(runID, last, msgs, err)
				r.afterTurn(ctx, &out)
				return out
			}
			req.Messages = compact
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
		r.emitEvent(ctx, runID, emit, Event{Type: EventStep, Step: step, Response: resp, RunID: runID})

		if len(resp.ToolCalls) == 0 {
			_ = r.Store.Delete(ctx, runID)
			r.appendCheckpoint(ctx, runID, checkpoint.NewEvent(checkpoint.TypeRunCompleted, runID).WithStep(step))
			out := outcomeDone(runID, resp, msgs)
			r.afterTurn(ctx, &out)
			return out
		}

		msgs = append(msgs, llm.Message{
			Role:      llm.Assistant,
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
			Source:    llm.SourceModel,
		})

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
		for i := range toolMsgs {
			toolMsgs[i].Source = llm.SourceTool
		}
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

func (r *Runner) toolIndexFrom(tools []Tool) map[string]Tool {
	byName := make(map[string]Tool, len(tools))
	for _, t := range tools {
		byName[t.Def.Name] = t
	}
	return byName
}

func (r *Runner) callModel(ctx context.Context, req llm.Request, step int, emit func(Event)) (*llm.Response, error) {
	if emit == nil && r.Hooks.OnChunk == nil {
		return r.Client.Generate(ctx, req)
	}

	var content strings.Builder
	var thinking strings.Builder
	var toolCalls []llm.ToolCall
	var usage llm.Usage

	for c, err := range r.Client.Stream(ctx, req) {
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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
		if c.ThinkingDelta != "" {
			thinking.WriteString(c.ThinkingDelta)
		}
		if len(c.ToolCalls) > 0 {
			toolCalls = c.ToolCalls
		}
		if c.Usage != nil {
			usage = *c.Usage
		}
		if c.Thinking != "" {
			thinking.Reset()
			thinking.WriteString(c.Thinking)
		}
	}

	resp := &llm.Response{
		Content:   content.String(),
		ToolCalls: toolCalls,
		Usage:     usage,
		Model:     req.Model,
		Thinking:  thinking.String(),
	}
	if len(req.Tools) > 0 && len(toolCalls) == 0 && content.Len() == 0 {
		return r.Client.Generate(ctx, req)
	}
	return resp, nil
}

func joinSys(base, extra string) string {
	if base == "" {
		return extra
	}
	return base + "\n\n" + extra
}

func (r *Runner) sessionID(ctx context.Context) string {
	if r.SessionID != "" {
		return r.SessionID
	}
	return r.Name
}

// collectMemberRun 消费子 Agent 事件流,转发中间事件,返回终态 RunOutcome。
func collectMemberRun(ctx context.Context, a Agent, req llm.Request, emit func(Event), opts ...Option) RunOutcome {
	for ev, err := range a.Run(ctx, req, opts...) {
		if err != nil {
			return outcomeError("", nil, nil, err)
		}
		switch ev.Type {
		case EventFinal:
			return outcomeDone(ev.RunID, ev.Response, nil)
		case EventPaused:
			return outcomePaused(ev.RunID, ev.Response, nil, ev.Requirements)
		case EventError:
			return outcomeError(ev.RunID, ev.Response, nil, ev.Err)
		default:
			if emit != nil {
				emit(ev)
			}
		}
	}
	return outcomeError("", nil, nil, errors.New("agent: run ended without terminal event"))
}

// collectMemberContinue 同 collectMemberRun,用于 Continue。
func collectMemberContinue(ctx context.Context, a Agent, runID string, resolutions []Resolution, emit func(Event), opts ...Option) RunOutcome {
	for ev, err := range a.Continue(ctx, runID, resolutions, opts...) {
		if err != nil {
			return outcomeError(runID, nil, nil, err)
		}
		switch ev.Type {
		case EventFinal:
			return outcomeDone(ev.RunID, ev.Response, nil)
		case EventPaused:
			return outcomePaused(ev.RunID, ev.Response, nil, ev.Requirements)
		case EventError:
			return outcomeError(ev.RunID, ev.Response, nil, ev.Err)
		default:
			if emit != nil {
				emit(ev)
			}
		}
	}
	return outcomeError(runID, nil, nil, errors.New("agent: continue ended without terminal event"))
}
