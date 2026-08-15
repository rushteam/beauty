package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

func TestMailbox_Steer_InjectBetweenTools(t *testing.T) {
	mb := agent.NewMailbox(4)
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo", Arguments: json.RawMessage(`{}`)}}},
		{Content: "after-steer"},
	}}
	var secondUsers []string
	r := &agent.Runner{
		Client:  fc,
		Tools:   []agent.Tool{echoTool()},
		Mailbox: mb,
		Hooks: agent.Hooks{
			AfterTool: func(_ context.Context, _ int, _ llm.ToolCall, _ *string) error {
				mb.Steer("请改成简短回答")
				return nil
			},
			BeforeModel: func(_ context.Context, step int, req *llm.Request) error {
				if step == 2 {
					for _, m := range req.Messages {
						if m.Role == llm.User {
							secondUsers = append(secondUsers, m.Content)
						}
					}
				}
				return nil
			},
		},
	}

	var sawSteer bool
	for ev, _ := range r.Run(context.Background(), llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.User, Content: "start"}}}) {
		if ev.Type == agent.EventSteer {
			sawSteer = true
		}
	}
	if !sawSteer {
		t.Fatal("missing EventSteer")
	}
	found := false
	for _, u := range secondUsers {
		if u == "请改成简短回答" {
			found = true
		}
	}
	if !found {
		t.Fatalf("step2 user msgs=%v", secondUsers)
	}
}

func TestHooks_BeforeAfter(t *testing.T) {
	var log []string
	fc := &fakeClient{steps: []*llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo", Arguments: json.RawMessage(`{"a":1}`)}}},
		{Content: "done"},
	}}
	r := &agent.Runner{
		Client: fc,
		Tools:  []agent.Tool{echoTool()},
		Hooks: agent.Hooks{
			BeforeModel: func(context.Context, int, *llm.Request) error {
				log = append(log, "bm")
				return nil
			},
			AfterModel: func(context.Context, int, *llm.Response) error {
				log = append(log, "am")
				return nil
			},
			BeforeTool: func(_ context.Context, _ int, _ *llm.ToolCall) (agent.Permission, error) {
				log = append(log, "bt")
				return agent.PermitAllow, nil
			},
			AfterTool: func(_ context.Context, _ int, _ llm.ToolCall, _ *string) error {
				log = append(log, "at")
				return nil
			},
		},
	}
	if _, err := agent.CollectOutcome(r.Run(context.Background(), llm.Request{Model: "m"})).Final(); err != nil {
		t.Fatal(err)
	}
	want := "bm am bt at bm am"
	got := ""
	for i, s := range log {
		if i > 0 {
			got += " "
		}
		got += s
	}
	if got != want {
		t.Fatalf("hooks=%q want %q", got, want)
	}
}

func TestHooks_BeforeModelError(t *testing.T) {
	want := errors.New("stop")
	r := &agent.Runner{
		Client: &fakeClient{steps: []*llm.Response{{Content: "x"}}},
		Hooks: agent.Hooks{
			BeforeModel: func(context.Context, int, *llm.Request) error { return want },
		},
	}
	out := agent.CollectOutcome(r.Run(context.Background(), llm.Request{Model: "m"}))
	_, err := out.Final()
	if !errors.Is(err, want) {
		t.Fatalf("got %v", err)
	}
}

func TestMailbox_Steer_EventType(t *testing.T) {
	mb := agent.NewMailbox(2)
	mb.Steer("hi-steer")
	fc := &fakeClient{steps: []*llm.Response{{Content: "ok"}}}
	r := &agent.Runner{Client: fc, Mailbox: mb}
	var saw bool
	for ev, _ := range r.Run(context.Background(), llm.Request{Model: "m"}) {
		if ev.Type == agent.EventSteer && ev.Result == "hi-steer" {
			saw = true
		}
	}
	if !saw {
		t.Fatal("missing EventSteer")
	}
}
