package agent

import (
	"context"
	"fmt"
	"iter"
	"sort"
	"strings"
	"sync"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent/checkpoint"
)

// handoffMarker 是成员在终态文本里请求移交的行前缀:HANDOFF: <成员名> <交给该成员的输入>。
const handoffMarker = "HANDOFF:"

// HandoffConfig 是多 agent 移交的 loop-safety 护栏参数。零值用默认。
type HandoffConfig struct {
	MaxHandoffs int
	Window      int
	MinUnique   int
}

func (c HandoffConfig) maxHandoffs() int {
	if c.MaxHandoffs > 0 {
		return c.MaxHandoffs
	}
	return 16
}

func (c HandoffConfig) window() int {
	if c.Window > 0 {
		return c.Window
	}
	return 4
}

func (c HandoffConfig) minUnique() int {
	if c.MinUnique > 0 {
		return c.MinUnique
	}
	return 2
}

type handoffTracker struct {
	cfg     HandoffConfig
	history []string
}

func (t *handoffTracker) record(target string) error {
	t.history = append(t.history, target)
	if len(t.history) > t.cfg.maxHandoffs() {
		return fmt.Errorf("agent: handoff guard: exceeded max handoffs (%d)", t.cfg.maxHandoffs())
	}
	w := t.cfg.window()
	if len(t.history) >= w {
		window := t.history[len(t.history)-w:]
		uniq := make(map[string]struct{}, w)
		for _, h := range window {
			uniq[h] = struct{}{}
		}
		if len(uniq) < t.cfg.minUnique() {
			return fmt.Errorf("agent: handoff guard: repetitive handoffs detected (last %d targets have only %d unique: %v)", w, len(uniq), window)
		}
	}
	return nil
}

// Team 多 agent 移交循环。成员 Paused 时整队 Paused。
type Team struct {
	Name    string
	Members map[string]Agent
	Entry   string
	Config  HandoffConfig
	Prompt  string
	Store   RunStore

	resumes sync.Map // runID → teamResume
}

type teamResume struct {
	member  Agent
	childID string
	current string
	input   llm.Request
	tracker *handoffTracker
	first   bool
	base    llm.Request
}

var _ Agent = (*Team)(nil)

func (tm *Team) ensureStore() {
	if tm.Store == nil {
		tm.Store = NewMemoryRunStore()
	}
}

func (tm *Team) cp() OrchestratorCheckpoint {
	return OrchestratorCheckpoint{Store: tm.Store, Name: tm.Name}
}

func (tm *Team) LoadRunTree(ctx context.Context, runID string) (*checkpoint.RunNode, error) {
	tm.ensureStore()
	return LoadRunTreeFromStore(ctx, tm.Store, runID)
}

func (tm *Team) LoadUIEvents(ctx context.Context, runID string) ([]checkpoint.Event, error) {
	tm.ensureStore()
	return LoadUIEventsFromStore(ctx, tm.Store, runID)
}

func (tm *Team) Run(ctx context.Context, req llm.Request, opts ...Option) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		tm.ensureStore()
		if len(tm.Members) == 0 {
			yield(Event{Type: EventError, Err: fmt.Errorf("agent: team has no members"), AgentName: tm.Name}, fmt.Errorf("agent: team has no members"))
			return
		}
		current := tm.Entry
		if current == "" {
			yield(Event{Type: EventError, Err: fmt.Errorf("agent: team entry is empty"), AgentName: tm.Name}, fmt.Errorf("agent: team entry is empty"))
			return
		}
		runID := newRunID()
		tracker := &handoffTracker{cfg: tm.Config}
		tm.cp().Started(ctx, runID, req)
		out := tm.loop(ctx, runID, req, current, req, tracker, true, yield, opts...)
		switch out.Status {
		case StatusDone:
			yield(Event{Type: EventFinal, Response: out.Response, RunID: out.RunID, AgentName: tm.Name}, nil)
		case StatusPaused:
			yield(Event{Type: EventPaused, Response: out.Response, RunID: out.RunID, Requirements: out.Requirements, AgentName: tm.Name}, nil)
		default:
			yield(Event{Type: EventError, Response: out.Response, RunID: out.RunID, Err: out.Err, AgentName: tm.Name}, out.Err)
		}
	}
}

func (tm *Team) loop(ctx context.Context, runID string, base llm.Request, current string, input llm.Request, tracker *handoffTracker, first bool, yield func(Event, error) bool, opts ...Option) RunOutcome {
	cp := tm.cp()
	prompt := tm.handoffPrompt()
	var last *llm.Response
	for {
		if err := ctx.Err(); err != nil {
			return outcomeError(runID, last, nil, err)
		}
		member, ok := tm.Members[current]
		if !ok {
			return outcomeError(runID, last, nil, fmt.Errorf("agent: team: unknown member %q", current))
		}

		stepReq := input
		if prompt != "" {
			if stepReq.System != "" {
				stepReq.System += "\n\n"
			}
			stepReq.System += prompt
		}
		runCtx := ctx
		if !first {
			runCtx = WithTrigger(ctx, TriggerTransfer, current)
		}
		src := "team:" + current

		out := tm.runMember(runCtx, member, current, stepReq, yield, opts...)
		cp.Spawned(ctx, runID, out.RunID, src, len(tracker.history))
		switch out.Status {
		case StatusPaused:
			reqs := remapRequirements(out.Requirements, src)
			snap := &RunSnapshot{
				Kind:          "team",
				Request:       base,
				Member:        current,
				ChildRunID:    out.RunID,
				ChildSource:   src,
				Requirements:  reqs,
				HandoffWindow: append([]string{}, tracker.history...),
			}
			if err := saveSnapshotWithCheckpoint(ctx, tm.Store, runID, snap); err != nil {
				return outcomeError(runID, out.Response, out.Messages, err)
			}
			cp.Paused(ctx, runID, len(tracker.history), out.Response, reqs, out.RunID, src)
			tm.resumes.Store(runID, teamResume{
				member: member, childID: out.RunID, current: current,
				input: input, tracker: tracker, first: first, base: base,
			})
			return outcomePaused(runID, out.Response, out.Messages, reqs)
		case StatusError:
			return outcomeError(runID, out.Response, out.Messages, out.Err)
		case StatusDone:
			last = out.Response
		default:
			return outcomeError(runID, out.Response, out.Messages, fmt.Errorf("agent: team unexpected status %q", out.Status))
		}

		target, handoffInput, ok := parseHandoff(respContent(last))
		if !ok {
			cp.Completed(ctx, runID)
			_ = tm.Store.Delete(ctx, runID)
			return outcomeDone(runID, last, nil)
		}
		if _, exists := tm.Members[target]; !exists {
			return outcomeError(runID, last, nil, fmt.Errorf("agent: team: member %q tried to hand off to unknown member %q", current, target))
		}
		cp.Handoff(ctx, runID, current, target, len(tracker.history))
		if err := tracker.record(target); err != nil {
			return outcomeError(runID, last, nil, err)
		}
		if handoffInput == "" {
			handoffInput = firstUserContent(input)
		}
		current = target
		input = llm.Request{Model: base.Model, Messages: []llm.Message{{Role: llm.User, Content: handoffInput}}}
		first = false
	}
}

func (tm *Team) runMember(ctx context.Context, member Agent, name string, req llm.Request, yield func(Event, error) bool, opts ...Option) RunOutcome {
	display := memberDisplayName(member, name)
	tt, tid := triggerFrom(ctx)
	var final RunOutcome
	for ev, err := range member.Run(ctx, req, opts...) {
		if err != nil {
			return outcomeError("", nil, nil, err)
		}
		if ev.AgentName == "" {
			ev.AgentName = display
		}
		ev.TriggerType = tt
		ev.TriggerID = tid
		switch ev.Type {
		case EventFinal:
			final = outcomeDone(ev.RunID, ev.Response, nil)
		case EventPaused:
			return outcomePaused(ev.RunID, ev.Response, nil, ev.Requirements)
		case EventError:
			return outcomeError(ev.RunID, ev.Response, nil, ev.Err)
		default:
			if !yield(ev, nil) {
				return outcomeError(ev.RunID, ev.Response, nil, ctx.Err())
			}
		}
	}
	if final.Status == StatusDone {
		return final
	}
	return outcomeError("", nil, nil, fmt.Errorf("agent: team member %q ended without final", name))
}

func (tm *Team) Continue(ctx context.Context, runID string, resolutions []Resolution, opts ...Option) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		out := tm.continueSync(ctx, runID, resolutions, yield, opts...)
		switch out.Status {
		case StatusDone:
			yield(Event{Type: EventFinal, Response: out.Response, RunID: out.RunID, AgentName: tm.Name}, nil)
		case StatusPaused:
			yield(Event{Type: EventPaused, Response: out.Response, RunID: out.RunID, Requirements: out.Requirements, AgentName: tm.Name}, nil)
		default:
			yield(Event{Type: EventError, Response: out.Response, RunID: out.RunID, Err: out.Err, AgentName: tm.Name}, out.Err)
		}
	}
}

func (tm *Team) continueSync(ctx context.Context, runID string, resolutions []Resolution, yield func(Event, error) bool, opts ...Option) RunOutcome {
	tm.ensureStore()
	cp := tm.cp()
	cp.Resumed(ctx, runID)
	rv, ok := tm.resumes.Load(runID)
	if !ok {
		return outcomeError(runID, nil, nil, fmt.Errorf("agent: team unknown runID %q", runID))
	}
	tr := rv.(teamResume)
	out := collectMemberContinue(ctx, tr.member, tr.childID, resolutions, func(e Event) {
		e.AgentName = memberDisplayName(tr.member, tr.current)
		yield(e, nil)
	}, opts...)
	switch out.Status {
	case StatusPaused:
		src := "team:" + tr.current
		reqs := remapRequirements(out.Requirements, src)
		snap := &RunSnapshot{
			Kind:          "team",
			Request:       tr.base,
			Member:        tr.current,
			ChildRunID:    out.RunID,
			ChildSource:   src,
			Requirements:  reqs,
			HandoffWindow: append([]string{}, tr.tracker.history...),
		}
		if err := saveSnapshotWithCheckpoint(ctx, tm.Store, runID, snap); err != nil {
			return outcomeError(runID, out.Response, out.Messages, err)
		}
		cp.Paused(ctx, runID, len(tr.tracker.history), out.Response, reqs, out.RunID, src)
		tm.resumes.Store(runID, teamResume{
			member: tr.member, childID: out.RunID, current: tr.current,
			input: tr.input, tracker: tr.tracker, first: tr.first, base: tr.base,
		})
		return outcomePaused(runID, out.Response, out.Messages, reqs)
	case StatusError:
		return outcomeError(runID, out.Response, out.Messages, out.Err)
	case StatusDone:
		tm.resumes.Delete(runID)
		last := out.Response
		target, handoffInput, isHandoff := parseHandoff(respContent(last))
		if !isHandoff {
			cp.Completed(ctx, runID)
			_ = tm.Store.Delete(ctx, runID)
			return outcomeDone(runID, last, nil)
		}
		if _, exists := tm.Members[target]; !exists {
			return outcomeError(runID, last, nil, fmt.Errorf("agent: team: member %q tried to hand off to unknown member %q", tr.current, target))
		}
		if err := tr.tracker.record(target); err != nil {
			return outcomeError(runID, last, nil, err)
		}
		if handoffInput == "" {
			handoffInput = firstUserContent(tr.input)
		}
		input := llm.Request{Model: tr.base.Model, Messages: []llm.Message{{Role: llm.User, Content: handoffInput}}}
		return tm.loop(ctx, runID, tr.base, target, input, tr.tracker, false, yield, opts...)
	default:
		return outcomeError(runID, out.Response, out.Messages, fmt.Errorf("agent: team unexpected status %q", out.Status))
	}
}

func memberDisplayName(m Agent, key string) string {
	if n := m.Info().Name; n != "" {
		return n
	}
	return key
}

func respContent(r *llm.Response) string {
	if r == nil {
		return ""
	}
	return r.Content
}

func (tm *Team) Info() Info {
	var tools []llm.ToolDef
	for _, name := range tm.memberNames() {
		tools = append(tools, tm.Members[name].Info().Tools...)
	}
	return Info{Name: tm.Name, Description: "multi-agent team", Tools: tools}
}

func (tm *Team) memberNames() []string {
	names := make([]string, 0, len(tm.Members))
	for n := range tm.Members {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (tm *Team) handoffPrompt() string {
	names := strings.Join(tm.memberNames(), ", ")
	if tm.Prompt != "" {
		return tm.Prompt + "\n可移交的成员:" + names + "。"
	}
	return "你是一个多 agent 团队的成员。若需要把任务移交给更合适的同伴,请另起一行,以\n" +
		handoffMarker + " <成员名> <交给该成员的输入>\n结尾(成员名取自:" + names + ")。" +
		"若你能直接完成任务,请直接给出最终答复,不要写 " + handoffMarker + "。"
}

func parseHandoff(content string) (target, input string, ok bool) {
	for ln := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimSpace(ln)
		rest, found := strings.CutPrefix(trimmed, handoffMarker)
		if !found {
			continue
		}
		rest = strings.TrimSpace(rest)
		if rest == "" {
			return "", "", false
		}
		if sp := strings.IndexAny(rest, " \t"); sp >= 0 {
			return rest[:sp], strings.TrimSpace(rest[sp+1:]), true
		}
		return rest, "", true
	}
	return "", "", false
}

func firstUserContent(req llm.Request) string {
	if len(req.Messages) > 0 {
		return req.Messages[0].Content
	}
	return ""
}
