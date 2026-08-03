package agent_test

import (
	"context"
	"errors"
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
func (c constClient) Stream(context.Context, llm.Request) (<-chan llm.Chunk, error) {
	return nil, errors.New("unused")
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
	resp, err := tm.Run(context.Background(), llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "研究主题 X"}}})
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
	_, err := tm.Run(context.Background(), llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "x"}}})
	if err == nil || !strings.Contains(err.Error(), "unknown member") {
		t.Fatalf("want unknown member error, got %v", err)
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
	_, err := tm.Run(context.Background(), llm.Request{Messages: []llm.Message{{Role: llm.User, Content: "start"}}})
	if err == nil || !strings.Contains(err.Error(), "max handoffs") {
		t.Fatalf("want max handoffs guard error, got %v", err)
	}
}
