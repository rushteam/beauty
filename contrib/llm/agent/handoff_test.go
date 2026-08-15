package agent_test

import (
	"context"
	"iter"
	"strings"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

// constClient 每次都返回固定内容(用于会多次被调用的成员,避免 fakeClient 脚本耗尽)。
type constClient struct{ content string }

func (c constClient) Generate(context.Context, llm.Request) (*llm.Response, error) {
	return &llm.Response{Content: c.content}, nil
}
func (c constClient) Stream(context.Context, llm.Request) iter.Seq2[llm.Chunk, error] {
	return unusedStream()
}

// 正常移交:researcher 交给 writer,writer 给出终态。
func TestTeam_Handoff(t *testing.T) {
	researcher := &agent.Runner{Name: "researcher", Client: &fakeClient{steps: []*llm.Response{
		{Content: "HANDOFF: writer 请把调研写成报告"},
	}}}
	writer := &agent.Runner{Name: "writer", Client: &fakeClient{steps: []*llm.Response{
		{Content: "final report"},
	}}}
	tm := &agent.Team{
		Members: map[string]agent.Agent{"researcher": researcher, "writer": writer},
		Entry:   "researcher",
	}
	out := agent.CollectOutcome(tm.Run(context.Background(), llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "研究主题 X"}}}))
	resp, err := out.Final()
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "final report" {
		t.Fatalf("content = %q, want final report", resp.Content)
	}
}

// 移交到未知成员应报错。
func TestTeam_UnknownTarget(t *testing.T) {
	a := &agent.Runner{Name: "a", Client: &fakeClient{steps: []*llm.Response{{Content: "HANDOFF: ghost hi"}}}}
	tm := &agent.Team{Members: map[string]agent.Agent{"a": a}, Entry: "a"}
	out := agent.CollectOutcome(tm.Run(context.Background(), llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "x"}}}))
	_, err := out.Final()
	if err == nil || !strings.Contains(err.Error(), "unknown member") {
		t.Fatalf("want unknown member error, got %v", err)
	}
}

// RunStream 流式:中间成员的 final 被内部消费,全程对外仅一条终态 EventFinal;
// transfer 归因正确(entry 成员为 user,被移交的成员为 transfer)。
func TestTeam_RunStream(t *testing.T) {
	researcher := &agent.Runner{Name: "researcher", Client: &fakeClient{steps: []*llm.Response{
		{Content: "HANDOFF: writer 写报告"},
	}}}
	writer := &agent.Runner{Name: "writer", Client: &fakeClient{steps: []*llm.Response{
		{Content: "final report"},
	}}}
	tm := &agent.Team{
		Members: map[string]agent.Agent{"researcher": researcher, "writer": writer},
		Entry:   "researcher",
	}

	var finals []agent.Event
	sawResearcherUser, sawWriterTransfer := false, false
	for ev, _ := range tm.Run(context.Background(), llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "研究 X"}}}) {
		if ev.Type == agent.EventError {
			t.Fatalf("unexpected error event: %v", ev.Err)
		}
		switch ev.AgentName {
		case "researcher":
			if ev.TriggerType == agent.TriggerUser {
				sawResearcherUser = true
			}
		case "writer":
			if ev.TriggerType == agent.TriggerTransfer && ev.TriggerID == "writer" {
				sawWriterTransfer = true
			}
		}
		if ev.Type == agent.EventFinal {
			finals = append(finals, ev)
		}
	}
	if len(finals) != 1 {
		t.Fatalf("应恰好一条终态 EventFinal, got %d", len(finals))
	}
	if finals[0].Response.Content != "final report" || finals[0].AgentName != "writer" {
		t.Fatalf("终态 = %q by %q, want \"final report\" by writer", finals[0].Response.Content, finals[0].AgentName)
	}
	if !sawResearcherUser {
		t.Error("researcher 的事件应带 TriggerUser 归因")
	}
	if !sawWriterTransfer {
		t.Error("writer 的事件应带 TriggerTransfer/writer 归因")
	}
}

// A↔B 打转:MaxHandoffs 护栏应在有限次后停机。
func TestTeam_MaxHandoffsGuard(t *testing.T) {
	a := &agent.Runner{Name: "a", Client: constClient{content: "HANDOFF: b to-b"}}
	b := &agent.Runner{Name: "b", Client: constClient{content: "HANDOFF: a to-a"}}
	tm := &agent.Team{
		Members: map[string]agent.Agent{"a": a, "b": b},
		Entry:   "a",
		Config:  agent.HandoffConfig{MaxHandoffs: 3},
	}
	out := agent.CollectOutcome(tm.Run(context.Background(), llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "start"}}}))
	_, err := out.Final()
	if err == nil || !strings.Contains(err.Error(), "max handoffs") {
		t.Fatalf("want max handoffs guard error, got %v", err)
	}
}
