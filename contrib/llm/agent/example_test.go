package agent_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

// 下面是可 `go test` 运行的离线示例(用确定性 stub client,无需 API key),
// 演示统一 Agent 接口、ReActPlanner、Team 移交、BestOfN 与 VerifyLoop 的用法与组合。

// exScript 按脚本逐次返回响应(单 goroutine 使用)。
type exScript struct {
	outs []string
	i    int
}

func (c *exScript) Generate(context.Context, llm.Request) (*llm.Response, error) {
	out := c.outs[c.i%len(c.outs)]
	c.i++
	return &llm.Response{Content: out}, nil
}
func (c *exScript) Stream(context.Context, llm.Request) (<-chan llm.Chunk, error) {
	return nil, errors.New("unused")
}

// exConst 每次都返回固定内容。
type exConst struct{ content string }

func (c exConst) Generate(context.Context, llm.Request) (*llm.Response, error) {
	return &llm.Response{Content: c.content}, nil
}
func (c exConst) Stream(context.Context, llm.Request) (<-chan llm.Chunk, error) {
	return nil, errors.New("unused")
}

// exPool 线程安全地轮流返回候选(BestOfN 会并发调用)。
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
func (c *exPool) Stream(context.Context, llm.Request) (<-chan llm.Chunk, error) {
	return nil, errors.New("unused")
}

// ReActPlanner:注入 ReAct 规划指令,并在模型以 "FINAL ANSWER:" 收尾时把响应收敛为干净答复。
func ExampleReActPlanner() {
	client := &exScript{outs: []string{
		"/*PLANNING*/ 先拆解问题\n/*REASONING*/ 6 乘 7\nFINAL ANSWER: 42",
	}}
	r := &agent.Runner{Client: client, Planner: &agent.ReActPlanner{}}

	out := r.Run(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "6*7=?"}},
	})
	resp, _ := out.Final()
	fmt.Println(resp.Content)
	// Output: 42
}

// Team:成员用 "HANDOFF: <成员名> <输入>" 把控制权移交同伴;无标记即终态。
// 每次移交都过 loop-safety 护栏(MaxHandoffs + 滑动窗口重复检测)。
func ExampleTeam() {
	researcher := &agent.Runner{Name: "researcher", Client: &exScript{outs: []string{
		"HANDOFF: writer 请把调研结论写成一段报告",
	}}}
	writer := &agent.Runner{Name: "writer", Client: exConst{content: "报告:方案 A 最优。"}}

	team := &agent.Team{
		Members: map[string]agent.Agent{"researcher": researcher, "writer": writer},
		Entry:   "researcher",
	}
	out := team.Run(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "调研主题 X"}},
	})
	resp, _ := out.Final()
	fmt.Println(resp.Content)
	// Output: 报告:方案 A 最优。
}

// BestOfN:并行采样 N 个候选,用 Selector 选最佳。默认 LongestSelector 取最长非空 Content。
func ExampleBestOfN() {
	sub := &agent.Runner{Client: &exPool{outs: []string{"短", "中等", "最长的候选答案"}}}
	best := &agent.BestOfN{Agent: sub, N: 3} // Select 为 nil → LongestSelector

	out := best.Run(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "给个答复"}},
	})
	resp, _ := out.Final()
	fmt.Println(resp.Content)
	// Output: 最长的候选答案
}

// VerifyLoop:跑→校验→带反馈重跑,直到通过或到 MaxRounds。校验逻辑由使用方注入。
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
	out := loop.Run(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "写点东西"}},
	})
	resp, _ := out.Final()
	fmt.Println(resp.Content)
	// Output: 修订稿 OK
}

// 统一 Agent 接口的收益:策略包装器可任意嵌套。这里把 BestOfN 的结果再交给 VerifyLoop 校验。
func Example_nesting() {
	inner := &agent.Runner{Client: &exPool{outs: []string{"x", "yy", "zzz OK"}}}
	best := &agent.BestOfN{Agent: inner, N: 3} // BestOfN 实现 Agent
	loop := &agent.VerifyLoop{                 // VerifyLoop 再包一层,同样实现 Agent
		Agent: best,
		Verify: func(_ context.Context, resp *llm.Response) (bool, string, error) {
			return strings.Contains(resp.Content, "OK"), "需含 OK", nil
		},
	}
	out := loop.Run(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "q"}},
	})
	resp, _ := out.Final()
	fmt.Println(resp.Content)
	// Output: zzz OK
}
