package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/rushteam/beauty/contrib/llm"
)

// ==== 并发扇出组合体:Parallel ====
//
// Parallel 把若干**不同的** Agent 并发跑同一个 Request,再用 Combine 把各自的终态响应合并成一个。
// 它补齐了可组合 Agent 家族里"并发"这一维度:
//   - Chain    串行:上一步输出喂下一步;
//   - Team     路由:按 HANDOFF 标记在成员间移交控制权;
//   - BestOfN  同一 Agent 跑 N 次、择优;
//   - Parallel 不同 Agent 并发跑、合并。
//
// 机制而非策略:如何合并(拼接?投票?再交给一个模型综合?)是 policy,由使用方注入 Combine;
// 本类型只负责并发调度、错误聚合与事件扇入。Parallel 自身也实现 Agent/StreamAgent,可被 Chain /
// AgentAsTool / Team 再嵌套(例如"并发调研 → 单 agent 汇总"就是 Chain{Parallel, summarizer})。

// Combiner 把并发跑完的多个响应合并成一个终态响应。cands 与 Parallel.Agents 下标一一对应,
// 失败或 nil 的分支在对应位置为 nil;调用时保证至少有一个非 nil。
type Combiner func(ctx context.Context, req llm.Request, cands []*llm.Response) (*llm.Response, error)

// ConcatCombiner 是平凡默认合并器:按 Agents 顺序把各非空响应的 Content 用空行连接,
// Usage 汇总相加。适合"把并行结果拼给下游再综合"的场景。
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

// Parallel 并发运行 Agents 中的每个 Agent(各收到相同 Request),再用 Combine 合并结果。
// Combine 为 nil 时用 ConcatCombiner。任一分支出错不影响其他分支;全部失败时返回聚合错误。
type Parallel struct {
	Name    string
	Agents  []Agent
	Combine Combiner
}

var (
	_ Agent       = (*Parallel)(nil)
	_ StreamAgent = (*Parallel)(nil)
)

// runAll 并发跑所有分支,返回与 Agents 一一对应的响应与错误(失败分支响应可能为 nil)。
func (p *Parallel) runAll(ctx context.Context, req llm.Request) (resps []*llm.Response, errs []error) {
	resps = make([]*llm.Response, len(p.Agents))
	errs = make([]error, len(p.Agents))
	var wg sync.WaitGroup
	for i := range p.Agents {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resps[i], errs[i] = p.Agents[i].Run(ctx, req)
		}(i)
	}
	wg.Wait()
	return resps, errs
}

// combine 过滤出成功分支交给 Combine;全部失败返回聚合错误。
func (p *Parallel) combine(ctx context.Context, req llm.Request, resps []*llm.Response, errs []error) (*llm.Response, error) {
	var firstErr error
	any := false
	for i := range resps {
		if errs[i] != nil {
			if firstErr == nil {
				firstErr = errs[i]
			}
			resps[i] = nil
			continue
		}
		if resps[i] != nil {
			any = true
		}
	}
	if !any {
		if firstErr != nil {
			return nil, fmt.Errorf("agent: Parallel all %d branches failed: %w", len(p.Agents), firstErr)
		}
		return nil, fmt.Errorf("agent: Parallel produced no responses")
	}
	comb := p.Combine
	if comb == nil {
		comb = ConcatCombiner
	}
	resp, err := comb(ctx, req, resps)
	if err != nil {
		return nil, fmt.Errorf("agent: Parallel combine: %w", err)
	}
	return resp, nil
}

// Run 并发跑所有分支并合并结果。
func (p *Parallel) Run(ctx context.Context, req llm.Request) (*llm.Response, error) {
	if len(p.Agents) == 0 {
		return nil, fmt.Errorf("agent: Parallel has no agents")
	}
	resps, errs := p.runAll(ctx, req)
	return p.combine(ctx, req, resps, errs)
}

// RunStream 实现 StreamAgent:并发跑各分支,支持流式的分支透传其中间事件(token/step/tool,
// 已由分支自行打好归因),不支持的分支同步跑;各分支的 final/error 内部捕获。全部结束后合并,
// 对外只产出一条终态 EventFinal(合并结果)。任一分支或合并出错则产出 EventError。
func (p *Parallel) RunStream(ctx context.Context, req llm.Request) <-chan Event {
	ch := make(chan Event, 32)
	go func() {
		defer close(ch)
		if len(p.Agents) == 0 {
			ch <- Event{Type: EventError, AgentName: p.Name, Err: fmt.Errorf("agent: Parallel has no agents")}
			return
		}
		resps := make([]*llm.Response, len(p.Agents))
		errs := make([]error, len(p.Agents))
		var wg sync.WaitGroup
		for i := range p.Agents {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				fev, err := p.streamBranch(ctx, p.Agents[i], req, ch)
				resps[i], errs[i] = fev.Response, err
			}(i)
		}
		wg.Wait()

		resp, err := p.combine(ctx, req, resps, errs)
		if err != nil {
			ch <- Event{Type: EventError, AgentName: p.Name, Err: err}
			return
		}
		ch <- Event{Type: EventFinal, AgentName: p.Name, Response: resp}
	}()
	return ch
}

// streamBranch 跑单个分支:支持 StreamAgent 时透传中间事件、捕获 final/error;否则同步跑并合成 final。
func (p *Parallel) streamBranch(ctx context.Context, a Agent, req llm.Request, ch chan<- Event) (Event, error) {
	if sa, ok := a.(StreamAgent); ok {
		var fev Event
		var rerr error
		for ev := range sa.RunStream(ctx, req) {
			switch ev.Type {
			case EventFinal:
				fev = ev
			case EventError:
				fev, rerr = ev, ev.Err
			default:
				ch <- ev
			}
		}
		return fev, rerr
	}
	resp, err := a.Run(ctx, req)
	tt, tid := triggerFrom(ctx)
	return Event{Type: EventFinal, Response: resp, AgentName: a.Info().Name, TriggerType: tt, TriggerID: tid}, err
}

// Info 实现 Agent:汇总各分支暴露的工具声明。
func (p *Parallel) Info() Info {
	var tools []llm.ToolDef
	for _, a := range p.Agents {
		if a != nil {
			tools = append(tools, a.Info().Tools...)
		}
	}
	return Info{Name: p.Name, Description: "parallel fan-out", Tools: tools}
}
