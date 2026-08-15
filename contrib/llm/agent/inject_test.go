package agent_test

import (
	"context"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

func TestMailbox_InjectSystem(t *testing.T) {
	mb := agent.NewMailbox(4)

	var systems []string
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo"}}},
		{Content: "done"},
	}}
	r := &agent.Runner{
		Client:  fc,
		Tools:   []agent.Tool{echoTool()},
		Mailbox: mb,
		Hooks: agent.Hooks{
			BeforeModel: func(_ context.Context, step int, req *llm.Request) error {
				systems = append(systems, req.System)
				return nil
			},
		},
	}

	mb.Inject("一次性上下文")
	mb.InjectPersistent("持久上下文")

	_, err := agent.CollectOutcome(r.Run(context.Background(), llm.Request{
		Model:  "m",
		System: "基础系统提示",
	})).Final()
	if err != nil {
		t.Fatal(err)
	}

	if len(systems) < 2 {
		t.Fatalf("expected at least 2 systems, got %d", len(systems))
	}
	// step 1: 基础 + 持久 + 一次性
	if s := systems[0]; s != "基础系统提示\n\n持久上下文\n一次性上下文" {
		t.Errorf("step1 system = %q", s)
	}
}

func TestMailbox_Nil(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{{Content: "ok"}}}
	r := &agent.Runner{Client: fc}
	out := agent.CollectOutcome(r.Run(context.Background(), llm.Request{Model: "m"}))
	if _, err := out.Final(); err != nil {
		t.Fatal(err)
	}
}
