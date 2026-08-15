package agent_test

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

// errClient 的 Generate 总是失败(用于模拟分支失败)。
type errClient struct{}

func (errClient) Generate(context.Context, llm.Request) (*llm.Response, error) {
	return nil, errors.New("boom")
}
func (errClient) Stream(context.Context, llm.Request) iter.Seq2[llm.Chunk, error] {
	return unusedStream()
}

// 默认 ConcatCombiner:按 Agents 顺序拼接各分支 Content。
func TestParallel_DefaultConcat(t *testing.T) {
	p := &agent.Parallel{Agents: []agent.Agent{
		&agent.Runner{Name: "a", Client: constClient{content: "结果A"}},
		&agent.Runner{Name: "b", Client: constClient{content: "结果B"}},
	}}
	out := agent.CollectOutcome(p.Run(context.Background(), llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "q"}}}))
	resp, err := out.Final()
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "结果A\n\n结果B" {
		t.Fatalf("content = %q", resp.Content)
	}
}

// 自定义 Combiner:可任意综合(这里选最长)。
func TestParallel_CustomCombiner(t *testing.T) {
	p := &agent.Parallel{
		Agents: []agent.Agent{
			&agent.Runner{Client: constClient{content: "短"}},
			&agent.Runner{Client: constClient{content: "较长的答案"}},
		},
		Combine: func(_ context.Context, _ llm.Request, cands []*llm.Response) (*llm.Response, error) {
			best := cands[0]
			for _, c := range cands {
				if c != nil && (best == nil || len(c.Content) > len(best.Content)) {
					best = c
				}
			}
			return best, nil
		},
	}
	out := agent.CollectOutcome(p.Run(context.Background(), llm.Request{}))
	resp, err := out.Final()
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "较长的答案" {
		t.Fatalf("content = %q", resp.Content)
	}
}

// 部分分支失败:失败分支被过滤,仍能合并成功分支。
func TestParallel_PartialFailure(t *testing.T) {
	p := &agent.Parallel{Agents: []agent.Agent{
		&agent.Runner{Client: errClient{}},
		&agent.Runner{Client: constClient{content: "幸存"}},
	}}
	out := agent.CollectOutcome(p.Run(context.Background(), llm.Request{}))
	resp, err := out.Final()
	if err != nil {
		t.Fatalf("部分失败仍应合并成功分支: %v", err)
	}
	if resp.Content != "幸存" {
		t.Fatalf("content = %q", resp.Content)
	}
}

// 全部失败:返回聚合错误。
func TestParallel_AllFail(t *testing.T) {
	p := &agent.Parallel{Agents: []agent.Agent{
		&agent.Runner{Client: errClient{}},
		&agent.Runner{Client: errClient{}},
	}}
	if out := agent.CollectOutcome(p.Run(context.Background(), llm.Request{})); !out.IsDone() {
		t.Fatal("全部失败应报错")
	}
}

func TestParallel_RunStream(t *testing.T) {
	a := &agent.Runner{Name: "a", Client: &streamScriptClient{streams: [][]llm.Chunk{{{Delta: "A1"}}}}}
	b := &agent.Runner{Name: "b", Client: &streamScriptClient{streams: [][]llm.Chunk{{{Delta: "B1"}}}}}
	p := &agent.Parallel{Agents: []agent.Agent{a, b}}

	var finals []agent.Event
	tokens := map[string]int{}
	for ev, err := range p.Run(context.Background(), llm.Request{Model: "m"}) {
		switch ev.Type {
		case agent.EventError:
			t.Fatalf("unexpected error: %v", ev.Err)
		case agent.EventToken:
			tokens[ev.AgentName]++
		case agent.EventFinal:
			finals = append(finals, ev)
		}
	}
	if len(finals) != 1 {
		t.Fatalf("应恰好一条终态 EventFinal, got %d", len(finals))
	}
	// 合并结果应含两分支的内容(顺序稳定:a 在前)。
	if !strings.Contains(finals[0].Response.Content, "A1") || !strings.Contains(finals[0].Response.Content, "B1") {
		t.Fatalf("合并结果应含两分支内容: %q", finals[0].Response.Content)
	}
	if tokens["a"] == 0 || tokens["b"] == 0 {
		t.Fatalf("两分支的 token 事件都应透传: %v", tokens)
	}
}
