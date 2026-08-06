package agent_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

// seqClient 线程安全地按轮次返回不同内容(BestOfN 会并发调用,需 -race 安全)。
type seqClient struct {
	mu   sync.Mutex
	outs []string
	i    int
}

func (c *seqClient) Generate(context.Context, llm.Request) (*llm.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.outs[c.i%len(c.outs)]
	c.i++
	return &llm.Response{Content: out}, nil
}
func (c *seqClient) Stream(context.Context, llm.Request) (<-chan llm.Chunk, error) {
	return nil, errors.New("unused")
}

// 自定义 Selector 应能挑出目标候选(不依赖并发调度顺序:三种输出必然都出现在候选集中)。
func TestBestOfN_CustomSelector(t *testing.T) {
	sub := &agent.Runner{Client: &seqClient{outs: []string{"a", "bb", "pick-me"}}}
	sel := func(_ context.Context, _ llm.Request, cands []*llm.Response) (int, error) {
		for i, c := range cands {
			if c.Content == "pick-me" {
				return i, nil
			}
		}
		return 0, errors.New("pick-me 未出现在候选中")
	}
	b := &agent.BestOfN{Agent: sub, N: 3, Select: sel}
	out := b.Run(context.Background(), llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "q"}}})
	resp, err := out.Final()
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "pick-me" {
		t.Fatalf("selected = %q, want pick-me", resp.Content)
	}
}

// 默认 LongestSelector 选最长非空 Content。
func TestBestOfN_DefaultLongest(t *testing.T) {
	sub := &agent.Runner{Client: &seqClient{outs: []string{"a", "bb", "cccccc"}}}
	b := &agent.BestOfN{Agent: sub, N: 3}
	out := b.Run(context.Background(), llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "q"}}})
	resp, err := out.Final()
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "cccccc" {
		t.Fatalf("longest = %q, want cccccc", resp.Content)
	}
}

// N<=1 时直通。
func TestBestOfN_Passthrough(t *testing.T) {
	sub := &agent.Runner{Client: &fakeClient{steps: []*llm.Response{{Content: "once"}}}}
	b := &agent.BestOfN{Agent: sub, N: 1}
	out := b.Run(context.Background(), llm.Request{})
	resp, err := out.Final()
	if err != nil || resp.Content != "once" {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
}

// VerifyLoop:首轮不达标带反馈,次轮通过。
func TestVerifyLoop_RetryUntilPass(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{
		{Content: "attempt-1"},
		{Content: "attempt-2 good"},
	}}
	sub := &agent.Runner{Client: fc}
	rounds := 0
	v := &agent.VerifyLoop{
		Agent: sub,
		Verify: func(_ context.Context, resp *llm.Response) (bool, string, error) {
			rounds++
			if strings.Contains(resp.Content, "good") {
				return true, "", nil
			}
			return false, "请加上 good", nil
		},
	}
	out := v.Run(context.Background(), llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "写点东西"}}})
	resp, err := out.Final()
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "attempt-2 good" {
		t.Fatalf("content = %q, want attempt-2 good", resp.Content)
	}
	if rounds != 2 {
		t.Fatalf("verify 调用 %d 次, want 2", rounds)
	}
	if fc.genCalls != 2 {
		t.Fatalf("Generate 调用 %d 次, want 2", fc.genCalls)
	}
	// 次轮请求应把上一轮答复与反馈追加进来。
	last := fc.lastReq.Messages
	if len(last) != 3 || last[2].Content != "请加上 good" {
		t.Fatalf("次轮消息未追加反馈: %+v", last)
	}
}

// VerifyLoop:达到 MaxRounds 仍不过 → best-effort 返回最后响应,不报错。
func TestVerifyLoop_MaxRounds(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{{Content: "x"}, {Content: "y"}}}
	sub := &agent.Runner{Client: fc}
	v := &agent.VerifyLoop{
		Agent:     sub,
		MaxRounds: 2,
		Verify:    func(context.Context, *llm.Response) (bool, string, error) { return false, "nope", nil },
	}
	out := v.Run(context.Background(), llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "go"}}})
	resp, err := out.Final()
	if err != nil {
		t.Fatalf("MaxRounds 应 best-effort 返回, got err %v", err)
	}
	if resp.Content != "y" {
		t.Fatalf("last = %q, want y", resp.Content)
	}
}
