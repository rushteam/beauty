package agent_test

import (
	"context"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

// 默认(顶层)运行:事件带 AgentName,TriggerType 为 user。
func TestEvent_DefaultTrigger(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{{Content: "hi"}}}
	r := &agent.Runner{Name: "root", Client: fc}
	var n int
	for ev := range r.RunStream(context.Background(), llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "q"}}}) {
		n++
		if ev.AgentName != "root" {
			t.Fatalf("AgentName = %q, want root (ev=%s)", ev.AgentName, ev.Type)
		}
		if ev.TriggerType != agent.TriggerUser || ev.TriggerID != "" {
			t.Fatalf("trigger = (%s,%q), want (user,\"\")", ev.TriggerType, ev.TriggerID)
		}
	}
	if n == 0 {
		t.Fatal("无事件")
	}
}

// WithTrigger 标注后,该运行发出的每条事件都带对应 Trigger 元数据。
func TestEvent_WithTrigger(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{{Content: "hi"}}}
	r := &agent.Runner{Name: "child", Client: fc}
	ctx := agent.WithTrigger(context.Background(), agent.TriggerTransfer, "h1")
	sawFinal := false
	for ev := range r.RunStream(ctx, llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "q"}}}) {
		if ev.TriggerType != agent.TriggerTransfer || ev.TriggerID != "h1" {
			t.Fatalf("trigger = (%s,%q), want (transfer,h1) on %s", ev.TriggerType, ev.TriggerID, ev.Type)
		}
		if ev.Type == agent.EventFinal {
			sawFinal = true
		}
	}
	if !sawFinal {
		t.Fatal("未见 EventFinal")
	}
}
