package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/rushteam/beauty/contrib/llm"
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

var (
	_ Agent       = (*Team)(nil)
	_ StreamAgent = (*Team)(nil)
)

func (tm *Team) ensureStore() {
	if tm.Store == nil {
		tm.Store = NewMemoryRunStore()
	}
}

// Run 从 Entry 起循环直到无 HANDOFF 或 Paused/Error。
func (tm *Team) Run(ctx context.Context, req llm.Request) RunOutcome {
	tm.ensureStore()
	if len(tm.Members) == 0 {
		return outcomeError("", nil, nil, fmt.Errorf("agent: team has no members"))
	}
	current := tm.Entry
	if current == "" {
		return outcomeError("", nil, nil, fmt.Errorf("agent: team entry is empty"))
	}
	runID := newRunID()
	tracker := &handoffTracker{cfg: tm.Config}
	return tm.loop(ctx, runID, req, current, req, tracker, true)
}

func (tm *Team) loop(ctx context.Context, runID string, base llm.Request, current string, input llm.Request, tracker *handoffTracker, first bool) RunOutcome {
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
		out := member.Run(runCtx, stepReq)
		switch out.Status {
		case StatusPaused:
			src := "team:" + current
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
			_ = tm.Store.Save(ctx, runID, snap)
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
			_ = tm.Store.Delete(ctx, runID)
			return outcomeDone(runID, last, nil)
		}
		if _, exists := tm.Members[target]; !exists {
			return outcomeError(runID, last, nil, fmt.Errorf("agent: team: member %q tried to hand off to unknown member %q", current, target))
		}
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

// Continue 恢复暂停的成员,再继续移交循环。
func (tm *Team) Continue(ctx context.Context, runID string, resolutions []Resolution) RunOutcome {
	tm.ensureStore()
	rv, ok := tm.resumes.Load(runID)
	if !ok {
		return outcomeError(runID, nil, nil, fmt.Errorf("agent: team unknown runID %q", runID))
	}
	tr := rv.(teamResume)
	out := tr.member.Continue(ctx, tr.childID, resolutions)
	switch out.Status {
	case StatusPaused:
		src := "team:" + tr.current
		reqs := remapRequirements(out.Requirements, src)
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
		return tm.loop(ctx, runID, tr.base, target, input, tr.tracker, false)
	default:
		return outcomeError(runID, out.Response, out.Messages, fmt.Errorf("agent: team unexpected status %q", out.Status))
	}
}

// RunStream 流式跑团队:透传成员中间事件;中间成员 final 内吞用于解析 HANDOFF;
// 仅最终成员的 EventFinal 外发。Paused 时发 EventPaused。
func (tm *Team) RunStream(ctx context.Context, req llm.Request) <-chan Event {
	ch := make(chan Event, 32)
	go func() {
		defer close(ch)
		if len(tm.Members) == 0 {
			ch <- Event{Type: EventError, Err: fmt.Errorf("agent: team has no members")}
			return
		}
		current := tm.Entry
		if current == "" {
			ch <- Event{Type: EventError, Err: fmt.Errorf("agent: team entry is empty")}
			return
		}
		tracker := &handoffTracker{cfg: tm.Config}
		prompt := tm.handoffPrompt()
		input := req
		first := true
		for {
			if err := ctx.Err(); err != nil {
				ch <- Event{Type: EventError, Err: err}
				return
			}
			member, ok := tm.Members[current]
			if !ok {
				ch <- Event{Type: EventError, Err: fmt.Errorf("agent: team: unknown member %q", current)}
				return
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

			finalEv, paused, rerr := tm.streamMember(runCtx, member, current, stepReq, ch)
			if rerr != nil {
				finalEv.Type, finalEv.Err = EventError, rerr
				ch <- finalEv
				return
			}
			if paused {
				ch <- finalEv // EventPaused
				return
			}
			resp := finalEv.Response
			target, handoffInput, isHandoff := parseHandoff(respContent(resp))
			if !isHandoff {
				ch <- finalEv
				return
			}
			if _, exists := tm.Members[target]; !exists {
				finalEv.Type = EventError
				finalEv.Err = fmt.Errorf("agent: team: member %q tried to hand off to unknown member %q", current, target)
				ch <- finalEv
				return
			}
			if err := tracker.record(target); err != nil {
				finalEv.Type, finalEv.Err = EventError, err
				ch <- finalEv
				return
			}
			if handoffInput == "" {
				handoffInput = firstUserContent(input)
			}
			current = target
			input = llm.Request{Model: req.Model, Messages: []llm.Message{{Role: llm.User, Content: handoffInput}}}
			first = false
		}
	}()
	return ch
}

func (tm *Team) streamMember(ctx context.Context, member Agent, name string, req llm.Request, ch chan<- Event) (ev Event, paused bool, err error) {
	if sa, ok := member.(StreamAgent); ok {
		var fev Event
		for e := range sa.RunStream(ctx, req) {
			switch e.Type {
			case EventFinal:
				fev = e
			case EventPaused:
				return e, true, nil
			case EventError:
				return e, false, e.Err
			default:
				ch <- e
			}
		}
		return fev, false, nil
	}
	out := member.Run(ctx, req)
	tt, tid := triggerFrom(ctx)
	switch out.Status {
	case StatusDone:
		return Event{
			Type: EventFinal, Response: out.Response, RunID: out.RunID,
			AgentName: memberDisplayName(member, name), TriggerType: tt, TriggerID: tid,
		}, false, nil
	case StatusPaused:
		return Event{
			Type: EventPaused, Response: out.Response, RunID: out.RunID, Requirements: out.Requirements,
			AgentName: memberDisplayName(member, name), TriggerType: tt, TriggerID: tid,
		}, true, nil
	default:
		return Event{
			Type: EventError, Response: out.Response, RunID: out.RunID, Err: out.Err,
			AgentName: memberDisplayName(member, name), TriggerType: tt, TriggerID: tid,
		}, false, out.Err
	}
}

func memberDisplayName(m Agent, key string) string {
	if n := m.Info().Name; n != "" {
		return n
	}
	return key
}

// ContinueStream 是 Continue 的流式版。
func (tm *Team) ContinueStream(ctx context.Context, runID string, resolutions []Resolution) <-chan Event {
	return streamAgentOutcome(tm.Name, func(emit func(Event)) RunOutcome {
		return tm.Continue(ctx, runID, resolutions)
	})
}

func respContent(r *llm.Response) string {
	if r == nil {
		return ""
	}
	return r.Content
}

// Info 实现 Agent。
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
