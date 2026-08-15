package compaction_test

import (
	"context"
	"strings"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent/compaction"
)

func TestContextWindow_NoOpUnderBudget(t *testing.T) {
	cw := &compaction.ContextWindow{
		MaxInputTokens:        1000,
		ToolEvictionThreshold: 0.5,
		TruncationThreshold:   0.8,
		Estimate:              charTokens,
	}
	in := []llm.Message{
		{Role: llm.System, Content: "sys"},
		{Role: llm.User, Content: "hello"},
		{Role: llm.Assistant, Content: "hi"},
	}
	out, err := cw.Compact(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(in) {
		t.Fatalf("under budget should return unchanged, got %d messages", len(out))
	}
}

func TestContextWindow_Phase1EvictsToolChainsFirst(t *testing.T) {
	bigTool := strings.Repeat("T", 400)
	cw := &compaction.ContextWindow{
		MaxInputTokens:        500,
		ToolEvictionThreshold: 0.5, // 阶段 1 目标 250
		TruncationThreshold:   0.8, // 阶段 2 目标 400
		KeepRecentGroups:      1,
		Estimate:              charTokens,
	}
	in := []llm.Message{
		{Role: llm.System, Content: "s"},                   // 1
		{Role: llm.User, Content: strings.Repeat("U", 50)}, // 50
		{
			Role:      llm.Assistant,
			ToolCalls: []llm.ToolCall{{ID: "c1", Name: "run"}},
		}, // 0 (no content)
		{Role: llm.Tool, ToolCallID: "c1", Content: bigTool}, // 400 → chain total 400
		{Role: llm.User, Content: strings.Repeat("R", 60)},   // 60 — 最近 1 组,保留
	}
	// 总计约 511 > 500, 需压缩; 阶段 1 应逐出旧 tool chain,保留 user 回合。

	out, err := cw.Compact(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}

	for _, m := range out {
		if m.Role == llm.Tool {
			t.Fatal("phase 1 should evict tool chain before touching user turns")
		}
	}
	hasOldUser := false
	hasRecent := false
	for _, m := range out {
		if m.Role == llm.User && len(m.Content) == 50 {
			hasOldUser = true
		}
		if m.Role == llm.User && len(m.Content) == 60 {
			hasRecent = true
		}
	}
	if !hasOldUser {
		t.Fatal("older user turn should survive phase 1 tool eviction")
	}
	if !hasRecent {
		t.Fatal("recent user turn must be kept")
	}
	if compaction.TotalTokens(out, charTokens) > cw.MaxInputTokens {
		t.Fatalf("result should fit budget, tokens=%d", compaction.TotalTokens(out, charTokens))
	}
}

func TestContextWindow_Phase2EvictsAllGroups(t *testing.T) {
	cw := &compaction.ContextWindow{
		MaxInputTokens:        200,
		ToolEvictionThreshold: 0.5, // 100
		TruncationThreshold:   0.8, // 160
		KeepRecentGroups:      1,
		Estimate:              charTokens,
	}
	in := []llm.Message{
		{Role: llm.System, Content: "sys"},                      // 3
		{Role: llm.User, Content: strings.Repeat("A", 80)},      // 80
		{Role: llm.Assistant, Content: strings.Repeat("B", 80)}, // 80
		{Role: llm.User, Content: strings.Repeat("C", 80)},      // 80 — 最近分组
	}
	// 无 tool chain; 总计 323 > 200。阶段 1 无 tool chain 可逐出;
	// 阶段 2 应逐出旧 user/assistant,保留 system + 最近 user。

	out, err := cw.Compact(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}

	if len(out) < 2 {
		t.Fatalf("expected system + recent user, got %+v", out)
	}
	if out[0].Role != llm.System {
		t.Fatal("system must remain")
	}
	last := out[len(out)-1]
	if last.Role != llm.User || last.Content != strings.Repeat("C", 80) {
		t.Fatalf("recent user group must be preserved, got %+v", last)
	}
	for _, m := range out {
		if m.Content == strings.Repeat("A", 80) || m.Content == strings.Repeat("B", 80) {
			t.Fatal("phase 2 should evict older non-system groups")
		}
	}
}

func TestContextWindow_KeepRecentGroupsProtection(t *testing.T) {
	cw := &compaction.ContextWindow{
		MaxInputTokens:        100,
		ToolEvictionThreshold: 0.5,
		TruncationThreshold:   0.8,
		KeepRecentGroups:      3,
		Estimate:              charTokens,
	}
	in := []llm.Message{
		{Role: llm.User, Content: strings.Repeat("1", 40)},
		{Role: llm.User, Content: strings.Repeat("2", 40)},
		{Role: llm.User, Content: strings.Repeat("3", 40)},
		{Role: llm.User, Content: strings.Repeat("4", 40)},
	}
	// 160 tokens, 预算 100; 保留最近 3 组 → 最多逐出 1 组。

	out, err := cw.Compact(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}

	if len(out) != 3 {
		t.Fatalf("expected 3 protected recent groups, got %d: %+v", len(out), out)
	}
	if out[0].Content != strings.Repeat("2", 40) {
		t.Fatalf("oldest evictable group should be removed, got %q", out[0].Content)
	}
	if out[2].Content != strings.Repeat("4", 40) {
		t.Fatal("newest group must remain")
	}
}

func TestContextWindow_PipelineIntegration(t *testing.T) {
	p := &compaction.Pipeline{
		Estimate: charTokens,
		Strategies: []compaction.GroupStrategy{
			&compaction.ContextWindow{
				MaxInputTokens:        150,
				ToolEvictionThreshold: 0.5,
				TruncationThreshold:   0.8,
				KeepRecentGroups:      1,
				Estimate:              charTokens,
			},
		},
	}
	in := []llm.Message{
		{Role: llm.System, Content: "s"},
		{
			Role:      llm.Assistant,
			ToolCalls: []llm.ToolCall{{ID: "x", Name: "t"}},
		},
		{Role: llm.Tool, ToolCallID: "x", Content: strings.Repeat("z", 200)},
		{Role: llm.User, Content: "recent"},
	}
	// 约 207 tokens > 150 预算; 阶段 1 应逐出 tool chain。
	out, err := p.Compact(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].Content != "s" || out[1].Content != "recent" {
		t.Fatalf("pipeline+contextwindow: %+v", out)
	}
}
