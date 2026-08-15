package agent

import (
	"context"
	"fmt"
	"iter"
	"sync"

	"github.com/rushteam/beauty/contrib/llm"
)

// ==== Runner 策略包装器 ====

// Selector 从若干候选响应里挑一个。
type Selector func(ctx context.Context, req llm.Request, cands []*llm.Response) (int, error)

// LongestSelector 选 Content 最长的候选。
func LongestSelector(_ context.Context, _ llm.Request, cands []*llm.Response) (int, error) {
	best, bestLen := 0, -1
	for i, c := range cands {
		if c != nil && len(c.Content) > bestLen {
			best, bestLen = i, len(c.Content)
		}
	}
	return best, nil
}

// BestOfN 并行采样 N 次后择优。底层若 Paused,该候选视为失败(需 SyncHITL 包装)。
type BestOfN struct {
	Agent  Agent
	N      int
	Select Selector
}

var _ Agent = (*BestOfN)(nil)

func (b *BestOfN) Run(ctx context.Context, req llm.Request, opts ...Option) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		out := b.runSync(ctx, req, opts...)
		switch out.Status {
		case StatusDone:
			yield(Event{Type: EventFinal, Response: out.Response, RunID: out.RunID}, nil)
		case StatusPaused:
			yield(Event{Type: EventPaused, Response: out.Response, RunID: out.RunID, Requirements: out.Requirements}, nil)
		default:
			yield(Event{Type: EventError, Response: out.Response, RunID: out.RunID, Err: out.Err}, out.Err)
		}
	}
}

func (b *BestOfN) runSync(ctx context.Context, req llm.Request, opts ...Option) RunOutcome {
	if b.Agent == nil {
		return outcomeError("", nil, nil, fmt.Errorf("agent: BestOfN has nil Agent"))
	}
	n := b.N
	if n <= 1 {
		return CollectOutcome(b.Agent.Run(ctx, req, opts...))
	}

	outs := make([]RunOutcome, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			outs[i] = CollectOutcome(b.Agent.Run(ctx, req, opts...))
		}(i)
	}
	wg.Wait()

	var cands []*llm.Response
	var firstErr error
	runID := ""
	for i := range n {
		switch outs[i].Status {
		case StatusDone:
			if outs[i].Response != nil {
				cands = append(cands, outs[i].Response)
			}
			if runID == "" {
				runID = outs[i].RunID
			}
		case StatusPaused:
			if firstErr == nil {
				firstErr = fmt.Errorf("agent: BestOfN candidate paused; wrap inner with SyncHITL")
			}
		default:
			if firstErr == nil && outs[i].Err != nil {
				firstErr = outs[i].Err
			}
		}
	}
	if len(cands) == 0 {
		if firstErr != nil {
			return outcomeError(runID, nil, nil, fmt.Errorf("agent: BestOfN all %d candidates failed: %w", n, firstErr))
		}
		return outcomeError(runID, nil, nil, fmt.Errorf("agent: BestOfN produced no candidates"))
	}

	sel := b.Select
	if sel == nil {
		sel = LongestSelector
	}
	idx, err := sel(ctx, req, cands)
	if err != nil {
		return outcomeError(runID, nil, nil, fmt.Errorf("agent: BestOfN select: %w", err))
	}
	if idx < 0 || idx >= len(cands) {
		return outcomeError(runID, nil, nil, fmt.Errorf("agent: BestOfN selector returned out-of-range index %d (have %d)", idx, len(cands)))
	}
	return outcomeDone(runID, cands[idx], nil)
}

func (b *BestOfN) Continue(ctx context.Context, runID string, resolutions []Resolution, opts ...Option) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		err := fmt.Errorf("agent: BestOfN.Continue not supported")
		yield(Event{Type: EventError, RunID: runID, Err: err}, err)
	}
}

func (b *BestOfN) Info() Info {
	if b.Agent == nil {
		return Info{}
	}
	in := b.Agent.Info()
	in.Description = fmt.Sprintf("best-of-%d(%s)", b.N, in.Description)
	return in
}

// Verifier 校验一次响应是否达标。
type Verifier func(ctx context.Context, resp *llm.Response) (ok bool, feedback string, err error)

// VerifyLoop 跑→校验→带反馈重跑。底层 Paused 时向上返回 Paused。
type VerifyLoop struct {
	Agent     Agent
	Verify    Verifier
	MaxRounds int
	Store     RunStore
	resumes   sync.Map
}

var _ Agent = (*VerifyLoop)(nil)

type verifyResume struct {
	msgs    []llm.Message
	req     llm.Request
	round   int
	childID string
}

func (v *VerifyLoop) Run(ctx context.Context, req llm.Request, opts ...Option) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		out := v.runStart(ctx, req, opts...)
		switch out.Status {
		case StatusDone:
			yield(Event{Type: EventFinal, Response: out.Response, RunID: out.RunID}, nil)
		case StatusPaused:
			yield(Event{Type: EventPaused, Response: out.Response, RunID: out.RunID, Requirements: out.Requirements}, nil)
		default:
			yield(Event{Type: EventError, Response: out.Response, RunID: out.RunID, Err: out.Err}, out.Err)
		}
	}
}

func (v *VerifyLoop) runStart(ctx context.Context, req llm.Request, opts ...Option) RunOutcome {
	if v.Agent == nil {
		return outcomeError("", nil, nil, fmt.Errorf("agent: VerifyLoop has nil Agent"))
	}
	if v.Store == nil {
		v.Store = NewMemoryRunStore()
	}
	runID := newRunID()
	msgs := make([]llm.Message, len(req.Messages))
	copy(msgs, req.Messages)
	return v.runRounds(ctx, runID, req, msgs, 0, opts...)
}

func (v *VerifyLoop) runRounds(ctx context.Context, runID string, req llm.Request, msgs []llm.Message, startRound int, opts ...Option) RunOutcome {
	rounds := v.MaxRounds
	if rounds <= 0 {
		rounds = 3
	}
	var last *llm.Response
	for round := startRound; round < rounds; round++ {
		if err := ctx.Err(); err != nil {
			return outcomeError(runID, last, msgs, err)
		}
		req.Messages = msgs
		out := CollectOutcome(v.Agent.Run(ctx, req, opts...))
		switch out.Status {
		case StatusPaused:
			snap := &RunSnapshot{Kind: "verify", Request: req, Messages: cloneMessages(msgs), ChildRunID: out.RunID, Step: round, Requirements: out.Requirements}
			_ = v.Store.Save(ctx, runID, snap)
			v.resumes.Store(runID, verifyResume{msgs: cloneMessages(msgs), req: req, round: round, childID: out.RunID})
			return outcomePaused(runID, out.Response, out.Messages, out.Requirements)
		case StatusError:
			return outcomeError(runID, out.Response, out.Messages, out.Err)
		case StatusDone:
			last = out.Response
		default:
			return outcomeError(runID, out.Response, out.Messages, fmt.Errorf("agent: VerifyLoop unexpected status %q", out.Status))
		}
		if v.Verify == nil {
			return outcomeDone(runID, last, msgs)
		}
		ok, feedback, err := v.Verify(ctx, last)
		if err != nil {
			return outcomeError(runID, last, msgs, fmt.Errorf("agent: VerifyLoop verify: %w", err))
		}
		if ok {
			return outcomeDone(runID, last, msgs)
		}
		msgs = append(msgs,
			llm.Message{Role: llm.Assistant, Content: last.Content},
			llm.Message{Role: llm.User, Content: feedback},
		)
	}
	return outcomeDone(runID, last, msgs)
}

func (v *VerifyLoop) Continue(ctx context.Context, runID string, resolutions []Resolution, opts ...Option) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		out := v.continueSync(ctx, runID, resolutions, opts...)
		switch out.Status {
		case StatusDone:
			yield(Event{Type: EventFinal, Response: out.Response, RunID: out.RunID}, nil)
		case StatusPaused:
			yield(Event{Type: EventPaused, Response: out.Response, RunID: out.RunID, Requirements: out.Requirements}, nil)
		default:
			yield(Event{Type: EventError, Response: out.Response, RunID: out.RunID, Err: out.Err}, out.Err)
		}
	}
}

func (v *VerifyLoop) continueSync(ctx context.Context, runID string, resolutions []Resolution, opts ...Option) RunOutcome {
	if v.Store == nil {
		v.Store = NewMemoryRunStore()
	}
	rv, ok := v.resumes.Load(runID)
	if !ok {
		return outcomeError(runID, nil, nil, fmt.Errorf("agent: VerifyLoop unknown runID %q", runID))
	}
	vr := rv.(verifyResume)
	out := CollectOutcome(v.Agent.Continue(ctx, vr.childID, resolutions, opts...))
	switch out.Status {
	case StatusPaused:
		v.resumes.Store(runID, verifyResume{msgs: vr.msgs, req: vr.req, round: vr.round, childID: out.RunID})
		return outcomePaused(runID, out.Response, out.Messages, out.Requirements)
	case StatusError:
		return outcomeError(runID, out.Response, out.Messages, out.Err)
	case StatusDone:
		v.resumes.Delete(runID)
		msgs := cloneMessages(vr.msgs)
		last := out.Response
		if v.Verify != nil {
			ok, feedback, err := v.Verify(ctx, last)
			if err != nil {
				return outcomeError(runID, last, msgs, err)
			}
			if !ok {
				msgs = append(msgs,
					llm.Message{Role: llm.Assistant, Content: last.Content},
					llm.Message{Role: llm.User, Content: feedback},
				)
				return v.runRounds(ctx, runID, vr.req, msgs, vr.round+1, opts...)
			}
		}
		return outcomeDone(runID, last, msgs)
	default:
		return outcomeError(runID, out.Response, out.Messages, fmt.Errorf("agent: VerifyLoop unexpected status %q", out.Status))
	}
}

func (v *VerifyLoop) Info() Info {
	if v.Agent == nil {
		return Info{}
	}
	in := v.Agent.Info()
	in.Description = "verify-loop(" + in.Description + ")"
	return in
}
