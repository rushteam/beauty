package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent/checkpoint"
)

// ==== 并发扇出组合体:Parallel ====

// Combiner 把并发跑完的多个响应合并成一个终态响应。
type Combiner func(ctx context.Context, req llm.Request, cands []*llm.Response) (*llm.Response, error)

// ConcatCombiner 按序拼接非空 Content。
func ConcatCombiner(_ context.Context, _ llm.Request, cands []*llm.Response) (*llm.Response, error) {
	var parts []string
	var usage llm.Usage
	model := ""
	for _, c := range cands {
		if c == nil {
			continue
		}
		if c.Content != "" {
			parts = append(parts, c.Content)
		}
		usage.InputTokens += c.Usage.InputTokens
		usage.OutputTokens += c.Usage.OutputTokens
		if model == "" {
			model = c.Model
		}
	}
	return &llm.Response{Content: strings.Join(parts, "\n\n"), Usage: usage, Model: model}, nil
}

// Parallel 并发运行各 Agent;任一分支持Paused 则整体 Paused。
type Parallel struct {
	Name    string
	Agents  []Agent
	Combine Combiner
	Store   RunStore

	resumes sync.Map // runID → map[int]parallelBranch
}

type parallelBranch struct {
	agent   Agent
	childID string
}

var (
	_ Agent       = (*Parallel)(nil)
	_ StreamAgent = (*Parallel)(nil)
)

func (p *Parallel) ensureStore() {
	if p.Store == nil {
		p.Store = NewMemoryRunStore()
	}
}

func (p *Parallel) cp() OrchestratorCheckpoint {
	return OrchestratorCheckpoint{Store: p.Store, Name: p.Name}
}

// LoadRunTree 从 checkpoint 事件日志构建编排树。
func (p *Parallel) LoadRunTree(ctx context.Context, runID string) (*checkpoint.RunNode, error) {
	p.ensureStore()
	return LoadRunTreeFromStore(ctx, p.Store, runID)
}

// LoadUIEvents 读取 run 的全部 checkpoint 事件。
func (p *Parallel) LoadUIEvents(ctx context.Context, runID string) ([]checkpoint.Event, error) {
	p.ensureStore()
	return LoadUIEventsFromStore(ctx, p.Store, runID)
}

// Run 并发跑所有分支并合并;有 Paused 则暂停。
func (p *Parallel) Run(ctx context.Context, req llm.Request) RunOutcome {
	p.ensureStore()
	if len(p.Agents) == 0 {
		return outcomeError("", nil, nil, fmt.Errorf("agent: Parallel has no agents"))
	}
	runID := newRunID()
	p.cp().Started(ctx, runID, req)
	outs := make([]RunOutcome, len(p.Agents))
	var wg sync.WaitGroup
	for i := range p.Agents {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if p.Agents[i] == nil {
				outs[i] = outcomeError("", nil, nil, fmt.Errorf("agent: Parallel branch %d nil", i))
				return
			}
			outs[i] = p.Agents[i].Run(ctx, req)
		}(i)
	}
	wg.Wait()
	return p.collect(ctx, runID, req, outs)
}

func (p *Parallel) collect(ctx context.Context, runID string, req llm.Request, outs []RunOutcome) RunOutcome {
	cp := p.cp()
	done := make(map[int]RunOutcome)
	paused := make(map[int]string)
	var reqs []Requirement
	var firstPaused *RunOutcome
	var firstErr error

	for i, out := range outs {
		switch out.Status {
		case StatusDone:
			done[i] = out
		case StatusPaused:
			paused[i] = out.RunID
			src := fmt.Sprintf("parallel:%d", i)
			cp.Spawned(ctx, runID, out.RunID, src, i)
			reqs = append(reqs, remapRequirements(out.Requirements, src)...)
			if firstPaused == nil {
				o := out
				firstPaused = &o
			}
			p.storeBranch(runID, i, p.Agents[i], out.RunID)
		default:
			if firstErr == nil {
				firstErr = out.Err
				if firstErr == nil {
					firstErr = fmt.Errorf("agent: Parallel branch %d failed", i)
				}
			}
		}
	}

	if len(paused) > 0 {
		bo := make(map[int]RunOutcome, len(done))
		for k, v := range done {
			bo[k] = v
		}
		pb := make(map[int]string, len(paused))
		for k, v := range paused {
			pb[k] = v
		}
		snap := &RunSnapshot{
			Kind:           "parallel",
			Request:        req,
			Requirements:   reqs,
			BranchOutcomes: bo,
			PausedBranches: pb,
		}
		if err := saveSnapshotWithCheckpoint(ctx, p.Store, runID, snap); err != nil {
			return outcomeError(runID, nil, nil, err)
		}
		resp := (*llm.Response)(nil)
		if firstPaused != nil {
			resp = firstPaused.Response
		}
		cp.Paused(ctx, runID, 0, resp, reqs, "", "")
		return outcomePaused(runID, resp, nil, reqs)
	}

	if len(done) == 0 {
		if firstErr != nil {
			return outcomeError(runID, nil, nil, fmt.Errorf("agent: Parallel all %d branches failed: %w", len(p.Agents), firstErr))
		}
		return outcomeError(runID, nil, nil, fmt.Errorf("agent: Parallel produced no responses"))
	}

	resps := make([]*llm.Response, len(p.Agents))
	for i, out := range done {
		resps[i] = out.Response
	}
	// 失败分支保持 nil
	comb := p.Combine
	if comb == nil {
		comb = ConcatCombiner
	}
	resp, err := comb(ctx, req, resps)
	if err != nil {
		return outcomeError(runID, nil, nil, fmt.Errorf("agent: Parallel combine: %w", err))
	}
	cp.Completed(ctx, runID)
	return outcomeDone(runID, resp, nil)
}

func (p *Parallel) storeBranch(runID string, i int, a Agent, childID string) {
	v, _ := p.resumes.LoadOrStore(runID, &sync.Map{})
	m := v.(*sync.Map)
	m.Store(i, parallelBranch{agent: a, childID: childID})
}

// Continue 只恢复仍暂停的分支,齐了再 Combine。
func (p *Parallel) Continue(ctx context.Context, runID string, resolutions []Resolution) RunOutcome {
	p.ensureStore()
	cp := p.cp()
	cp.Resumed(ctx, runID)
	snap, err := loadSnapshotFromStore(ctx, p.Store, runID)
	if err != nil {
		return outcomeError(runID, nil, nil, err)
	}
	if snap == nil || snap.Kind != "parallel" {
		return outcomeError(runID, nil, nil, fmt.Errorf("agent: unknown parallel runID %q", runID))
	}
	rv, ok := p.resumes.Load(runID)
	if !ok {
		return outcomeError(runID, nil, nil, fmt.Errorf("agent: parallel resume lost for %q", runID))
	}
	branches := rv.(*sync.Map)

	done := snap.BranchOutcomes
	if done == nil {
		done = map[int]RunOutcome{}
	}
	paused := map[int]string{}
	var reqs []Requirement
	var firstPaused *RunOutcome

	branches.Range(func(k, val any) bool {
		i := k.(int)
		br := val.(parallelBranch)
		out := br.agent.Continue(ctx, br.childID, resolutions)
		switch out.Status {
		case StatusDone:
			done[i] = out
			branches.Delete(i)
		case StatusPaused:
			paused[i] = out.RunID
			src := fmt.Sprintf("parallel:%d", i)
			cp.Spawned(ctx, runID, out.RunID, src, i)
			reqs = append(reqs, remapRequirements(out.Requirements, src)...)
			branches.Store(i, parallelBranch{agent: br.agent, childID: out.RunID})
			if firstPaused == nil {
				o := out
				firstPaused = &o
			}
		default:
			// 记为失败:从 paused 去掉
			branches.Delete(i)
		}
		return true
	})

	if len(paused) > 0 {
		pb := make(map[int]string, len(paused))
		for k, v := range paused {
			pb[k] = v
		}
		snap.BranchOutcomes = done
		snap.PausedBranches = pb
		snap.Requirements = reqs
		if err := saveSnapshotWithCheckpoint(ctx, p.Store, runID, snap); err != nil {
			return outcomeError(runID, nil, nil, err)
		}
		resp := (*llm.Response)(nil)
		if firstPaused != nil {
			resp = firstPaused.Response
		}
		cp.Paused(ctx, runID, 0, resp, reqs, "", "")
		return outcomePaused(runID, resp, nil, reqs)
	}

	p.resumes.Delete(runID)
	cp.Completed(ctx, runID)
	_ = p.Store.Delete(ctx, runID)

	resps := make([]*llm.Response, len(p.Agents))
	any := false
	for i, out := range done {
		if out.Response != nil {
			resps[i] = out.Response
			any = true
		}
	}
	if !any {
		return outcomeError(runID, nil, nil, fmt.Errorf("agent: Parallel produced no responses after continue"))
	}
	comb := p.Combine
	if comb == nil {
		comb = ConcatCombiner
	}
	resp, err := comb(ctx, snap.Request, resps)
	if err != nil {
		return outcomeError(runID, nil, nil, err)
	}
	return outcomeDone(runID, resp, nil)
}

// RunStream 并发透传各分支中间事件;对外仅一条合并后的 EventFinal(或 Paused/Error)。
func (p *Parallel) RunStream(ctx context.Context, req llm.Request) <-chan Event {
	ch := make(chan Event, 32)
	go func() {
		defer close(ch)
		p.ensureStore()
		if len(p.Agents) == 0 {
			ch <- Event{Type: EventError, AgentName: p.Name, Err: fmt.Errorf("agent: Parallel has no agents")}
			return
		}
		runID := newRunID()
		outs := make([]RunOutcome, len(p.Agents))
		var wg sync.WaitGroup
		for i := range p.Agents {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				outs[i] = p.streamBranch(ctx, p.Agents[i], req, ch)
			}(i)
		}
		wg.Wait()
		out := p.collect(ctx, runID, req, outs)
		switch out.Status {
		case StatusDone:
			ch <- Event{Type: EventFinal, AgentName: p.Name, Response: out.Response, RunID: out.RunID}
		case StatusPaused:
			ch <- Event{Type: EventPaused, AgentName: p.Name, Response: out.Response, RunID: out.RunID, Requirements: out.Requirements}
		default:
			ch <- Event{Type: EventError, AgentName: p.Name, Response: out.Response, RunID: out.RunID, Err: out.Err}
		}
	}()
	return ch
}

func (p *Parallel) streamBranch(ctx context.Context, a Agent, req llm.Request, ch chan<- Event) RunOutcome {
	if a == nil {
		return outcomeError("", nil, nil, fmt.Errorf("agent: Parallel nil branch"))
	}
	if sa, ok := a.(StreamAgent); ok {
		var fev Event
		for ev := range sa.RunStream(ctx, req) {
			switch ev.Type {
			case EventFinal:
				fev = ev
			case EventPaused:
				return outcomePaused(ev.RunID, ev.Response, nil, ev.Requirements)
			case EventError:
				return outcomeError(ev.RunID, ev.Response, nil, ev.Err)
			default:
				ch <- ev
			}
		}
		if fev.Response != nil {
			return outcomeDone(fev.RunID, fev.Response, nil)
		}
		return outcomeError("", nil, nil, fmt.Errorf("agent: Parallel branch produced no final"))
	}
	return a.Run(ctx, req)
}

// ContinueStream 是 Continue 的流式版。
func (p *Parallel) ContinueStream(ctx context.Context, runID string, resolutions []Resolution) <-chan Event {
	ch := make(chan Event, 32)
	go func() {
		defer close(ch)
		out := p.Continue(ctx, runID, resolutions)
		switch out.Status {
		case StatusDone:
			ch <- Event{Type: EventFinal, AgentName: p.Name, Response: out.Response, RunID: out.RunID}
		case StatusPaused:
			ch <- Event{Type: EventPaused, AgentName: p.Name, Response: out.Response, RunID: out.RunID, Requirements: out.Requirements}
		default:
			ch <- Event{Type: EventError, AgentName: p.Name, Response: out.Response, RunID: out.RunID, Err: out.Err}
		}
	}()
	return ch
}

// Info 实现 Agent。
func (p *Parallel) Info() Info {
	var tools []llm.ToolDef
	for _, a := range p.Agents {
		if a != nil {
			tools = append(tools, a.Info().Tools...)
		}
	}
	return Info{Name: p.Name, Description: "parallel fan-out", Tools: tools}
}
