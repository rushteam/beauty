package agent_test

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"sync"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

type exScript struct {
	outs []string
	i    int
}

func (c *exScript) Generate(context.Context, llm.Request) (*llm.Response, error) {
	out := c.outs[c.i%len(c.outs)]
	c.i++
	return &llm.Response{Content: out}, nil
}

func (c *exScript) Stream(context.Context, llm.Request) iter.Seq2[llm.Chunk, error] {
	return unusedStream()
}

type exConst struct{ content string }

func (c exConst) Generate(context.Context, llm.Request) (*llm.Response, error) {
	return &llm.Response{Content: c.content}, nil
}

func (c exConst) Stream(context.Context, llm.Request) iter.Seq2[llm.Chunk, error] {
	return unusedStream()
}

type exPool struct {
	mu   sync.Mutex
	outs []string
	i    int
}

func (c *exPool) Generate(context.Context, llm.Request) (*llm.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.outs[c.i%len(c.outs)]
	c.i++
	return &llm.Response{Content: out}, nil
}

func (c *exPool) Stream(context.Context, llm.Request) iter.Seq2[llm.Chunk, error] {
	return unusedStream()
}

func ExampleReActPlanner() {
	client := &exScript{outs: []string{
		"/*PLANNING*/ 先拆解问题\n/*REASONING*/ 6 乘 7\nFINAL ANSWER: 42",
	}}
	r := &agent.Runner{Client: client, Planner: &agent.ReActPlanner{}}

	out := agent.CollectOutcome(r.Run(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "6*7=?"}},
	}))
	resp, _ := out.Final()
	fmt.Println(resp.Content)
	// Output: 42
}

func ExampleTeam() {
	researcher := &agent.Runner{Name: "researcher", Client: &exScript{outs: []string{
		"HANDOFF: writer 请把调研结论写成一段报告",
	}}}
	writer := &agent.Runner{Name: "writer", Client: exConst{content: "报告:方案 A 最优。"}}

	team := &agent.Team{
		Members: map[string]agent.Agent{"researcher": researcher, "writer": writer},
		Entry:   "researcher",
	}
	out := agent.CollectOutcome(team.Run(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "调研主题 X"}},
	}))
	resp, _ := out.Final()
	fmt.Println(resp.Content)
	// Output: 报告:方案 A 最优。
}

func ExampleBestOfN() {
	sub := &agent.Runner{Client: &exPool{outs: []string{"短", "中等", "最长的候选答案"}}}
	best := &agent.BestOfN{Agent: sub, N: 3}

	out := agent.CollectOutcome(best.Run(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "给个答复"}},
	}))
	resp, _ := out.Final()
	fmt.Println(resp.Content)
	// Output: 最长的候选答案
}

func ExampleVerifyLoop() {
	client := &exScript{outs: []string{"初稿", "修订稿 OK"}}
	loop := &agent.VerifyLoop{
		Agent: &agent.Runner{Client: client},
		Verify: func(_ context.Context, resp *llm.Response) (bool, string, error) {
			if strings.Contains(resp.Content, "OK") {
				return true, "", nil
			}
			return false, "请在结尾加上 OK 标记", nil
		},
	}
	out := agent.CollectOutcome(loop.Run(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "写点东西"}},
	}))
	resp, _ := out.Final()
	fmt.Println(resp.Content)
	// Output: 修订稿 OK
}

func Example_nesting() {
	inner := &agent.Runner{Client: &exPool{outs: []string{"x", "yy", "zzz OK"}}}
	best := &agent.BestOfN{Agent: inner, N: 3}
	loop := &agent.VerifyLoop{
		Agent: best,
		Verify: func(_ context.Context, resp *llm.Response) (bool, string, error) {
			return strings.Contains(resp.Content, "OK"), "需含 OK", nil
		},
	}
	out := agent.CollectOutcome(loop.Run(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "q"}},
	}))
	resp, _ := out.Final()
	fmt.Println(resp.Content)
	// Output: zzz OK
}
