package agent_test

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

// 基础轮转:2 个 agent,3 轮,按 A→B→A 发言。
func TestGroupChat_RoundRobin(t *testing.T) {
	a := &agent.Runner{Name: "alice", Client: constClient{content: "from-alice"}}
	b := &agent.Runner{Name: "bob", Client: constClient{content: "from-bob"}}
	gc := &agent.GroupChat{
		Name:          "room",
		Agents:        []agent.Agent{a, b},
		MaxIterations: 3,
	}

	var steps []string
	var final *llm.Response
	for ev, err := range gc.Run(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "start"}},
	}) {
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		switch ev.Type {
		case agent.EventStep:
			steps = append(steps, ev.AgentName)
		case agent.EventFinal:
			final = ev.Response
		}
	}

	want := []string{"alice", "bob", "alice"}
	if len(steps) != len(want) {
		t.Fatalf("steps = %v, want %v", steps, want)
	}
	for i := range want {
		if steps[i] != want[i] {
			t.Fatalf("step[%d] agent = %q, want %q", i, steps[i], want[i])
		}
	}
	if final == nil {
		t.Fatal("missing final response")
	}
	if final.Content != "from-alice\n\nfrom-bob\n\nfrom-alice" {
		t.Fatalf("final content = %q", final.Content)
	}
}

// ContentTerminator:关键词出现在历史中后终止。
func TestGroupChat_ContentTerminator(t *testing.T) {
	a := &agent.Runner{Name: "a", Client: constClient{content: "hello"}}
	b := &agent.Runner{Name: "b", Client: constClient{content: "please STOP now"}}
	gc := &agent.GroupChat{
		Agents:        []agent.Agent{a, b},
		MaxIterations: 10,
		Manager: agent.GroupChatManager{
			ShouldTerminate: agent.ContentTerminator("STOP"),
		},
	}

	out := agent.CollectOutcome(gc.Run(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "go"}},
	}))
	resp, err := out.Final()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if resp.Content != "please STOP now" {
		t.Fatalf("final = %q, want b's last response", resp.Content)
	}
}

// MaxIterationTerminator:达到指定轮数后终止(不再继续发言)。
func TestGroupChat_MaxIterationTerminator(t *testing.T) {
	a := &agent.Runner{Name: "a", Client: constClient{content: "a-says"}}
	b := &agent.Runner{Name: "b", Client: constClient{content: "b-says"}}
	gc := &agent.GroupChat{
		Agents: []agent.Agent{a, b},
		Manager: agent.GroupChatManager{
			ShouldTerminate: agent.MaxIterationTerminator(2),
		},
		MaxIterations: 10,
	}

	var stepCount int
	for ev, err := range gc.Run(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "go"}},
	}) {
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if ev.Type == agent.EventStep {
			stepCount++
		}
	}
	if stepCount != 2 {
		t.Fatalf("step count = %d, want 2", stepCount)
	}
}

// 自定义 SelectNextAgent:始终选择第一个 agent。
func TestGroupChat_CustomSelectNextAgent(t *testing.T) {
	a := &agent.Runner{Name: "always", Client: constClient{content: "only-a"}}
	b := &agent.Runner{Name: "never", Client: constClient{content: "only-b"}}
	gc := &agent.GroupChat{
		Agents:        []agent.Agent{a, b},
		MaxIterations: 3,
		Manager: agent.GroupChatManager{
			SelectNextAgent: func(_ context.Context, _ []llm.Message, agents []agent.Agent) (agent.Agent, error) {
				return agents[0], nil
			},
		},
	}

	var steps []string
	for ev, err := range gc.Run(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "go"}},
	}) {
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if ev.Type == agent.EventStep {
			steps = append(steps, ev.AgentName)
		}
	}
	for _, name := range steps {
		if name != "always" {
			t.Fatalf("expected always, got %q", name)
		}
	}
}

// 空 agents 列表应报错。
func TestGroupChat_EmptyAgents(t *testing.T) {
	gc := &agent.GroupChat{Name: "empty"}
	out := agent.CollectOutcome(gc.Run(context.Background(), llm.Request{}))
	if out.IsDone() {
		t.Fatal("empty agents should error")
	}
	if out.Err == nil {
		t.Fatal("expected error")
	}
}

// Continue 不支持。
func TestGroupChat_ContinueNotSupported(t *testing.T) {
	gc := &agent.GroupChat{
		Agents: []agent.Agent{&agent.Runner{Name: "a", Client: constClient{content: "x"}}},
	}
	var gotErr error
	for _, err := range gc.Continue(context.Background(), "run-1", nil) {
		gotErr = err
		break
	}
	if gotErr == nil {
		t.Fatal("Continue should return error")
	}
}

// countingClient 记录 Generate 调用次数。
type countingClient struct {
	content string
	calls   int
}

func (c *countingClient) Generate(_ context.Context, _ llm.Request) (*llm.Response, error) {
	c.calls++
	return &llm.Response{Content: c.content}, nil
}

func (c *countingClient) Stream(context.Context, llm.Request) iter.Seq2[llm.Chunk, error] {
	return unusedStream()
}

// SelectNextAgent 返回 nil 时结束会话。
func TestGroupChat_SelectNextReturnsNil(t *testing.T) {
	cc := &countingClient{content: "once"}
	a := &agent.Runner{Name: "a", Client: cc}
	gc := &agent.GroupChat{
		Agents:        []agent.Agent{a},
		MaxIterations: 5,
		Manager: agent.GroupChatManager{
			SelectNextAgent: func(_ context.Context, history []llm.Message, agents []agent.Agent) (agent.Agent, error) {
				for _, m := range history {
					if m.Role == llm.Assistant {
						return nil, nil
					}
				}
				return agents[0], nil
			},
		},
	}

	out := agent.CollectOutcome(gc.Run(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "go"}},
	}))
	resp, err := out.Final()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if cc.calls != 1 {
		t.Fatalf("Generate calls = %d, want 1", cc.calls)
	}
	if resp.Content != "once" {
		t.Fatalf("final = %q", resp.Content)
	}
}

// UpdateHistory 可过滤广播给其他参与者的历史。
func TestGroupChat_UpdateHistory(t *testing.T) {
	seen := false
	a := &agent.Runner{Name: "a", Client: &fakeClient{steps: []*llm.Response{{Content: "reply"}}}}
	gc := &agent.GroupChat{
		Agents:        []agent.Agent{a},
		MaxIterations: 1,
		Manager: agent.GroupChatManager{
			UpdateHistory: func(_ context.Context, history []llm.Message) ([]llm.Message, error) {
				seen = true
				if len(history) != 1 || history[0].Content != "secret" {
					return nil, errors.New("unexpected history")
				}
				return history, nil
			},
		},
	}

	if _, err := agent.CollectOutcome(gc.Run(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: llm.User, Content: "secret"}},
	})).Final(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !seen {
		t.Fatal("UpdateHistory was not called")
	}
}
