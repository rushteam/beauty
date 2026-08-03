package agent_test

import (
	"context"
	"strings"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

// ReActPlanner 应把规划指令注入 system,并在响应含 FINAL ANSWER: 时收敛为干净答复。
func TestReActPlanner_InjectAndConverge(t *testing.T) {
	fc := &fakeClient{steps: []*llm.Response{
		{Content: "/*PLANNING*/ 先想想\n/*REASONING*/ 因为\nFINAL ANSWER: 42"},
	}}
	r := &agent.Runner{Client: fc, Planner: &agent.ReActPlanner{}}
	resp, err := r.Run(context.Background(), llm.Request{
		System:   "be nice",
		Messages: []llm.Message{{Role: llm.User, Content: "q"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 指令注入到 system,且保留原有 system。
	if !strings.Contains(fc.lastReq.System, "be nice") {
		t.Fatalf("原 system 丢失: %q", fc.lastReq.System)
	}
	if !strings.Contains(fc.lastReq.System, "FINAL ANSWER:") || !strings.Contains(fc.lastReq.System, "ReAct") {
		t.Fatalf("规划指令未注入 system: %q", fc.lastReq.System)
	}
	// 终态收敛为 marker 之后的内容。
	if resp.Content != "42" {
		t.Fatalf("收敛后的答复 = %q, want 42", resp.Content)
	}
}

// 无 FINAL ANSWER: 时,ProcessPlanningResponse 剥离标记行、保留正文。
func TestReActPlanner_StripTags(t *testing.T) {
	p := &agent.ReActPlanner{}
	got := p.ProcessPlanningResponse(1, &llm.Response{
		Content: "/*PLANNING*/ 计划\n真正的正文\n/*ACTION*/ 动作",
	})
	if got.Content != "真正的正文" {
		t.Fatalf("stripTags = %q, want 真正的正文", got.Content)
	}
}

// FINAL ANSWER: 直接截取其后内容(优先于剥标记)。
func TestReActPlanner_FinalMarkerWins(t *testing.T) {
	p := &agent.ReActPlanner{}
	got := p.ProcessPlanningResponse(1, &llm.Response{
		Content: "/*REASONING*/ 推理\nFINAL ANSWER: 最终答复",
	})
	if got.Content != "最终答复" {
		t.Fatalf("final = %q, want 最终答复", got.Content)
	}
}

// KeepReasoning=true 时不剥标记(仅在有 FinalMarker 时截取)。
func TestReActPlanner_KeepReasoning(t *testing.T) {
	p := &agent.ReActPlanner{KeepReasoning: true}
	in := "/*PLANNING*/ 计划\n正文"
	got := p.ProcessPlanningResponse(1, &llm.Response{Content: in})
	if got.Content != in {
		t.Fatalf("KeepReasoning 应原样保留, got %q", got.Content)
	}
}
