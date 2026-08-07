package prompt

import (
	"context"
	"strings"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
)

func msgs(roles ...llm.Role) []llm.Message {
	out := make([]llm.Message, len(roles))
	for i, r := range roles {
		out[i] = llm.Message{Role: r, Content: string(r) + "_" + string(rune('0'+i))}
	}
	return out
}

func msg(role llm.Role, content string) llm.Message {
	return llm.Message{Role: role, Content: content}
}

func assertSystem(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("System:\n  got  = %q\n  want = %q", got, want)
	}
}

func assertMessages(t *testing.T, got []llm.Message, want ...llm.Message) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("Messages: len=%d, want %d\n  got:  %v\n  want: %v", len(got), len(want), fmtMsgs(got), fmtMsgs(want))
	}
	for i := range got {
		if got[i].Role != want[i].Role || got[i].Content != want[i].Content {
			t.Errorf("Messages[%d]:\n  got  = {%s, %q}\n  want = {%s, %q}", i, got[i].Role, got[i].Content, want[i].Role, want[i].Content)
		}
	}
}

func fmtMsgs(msgs []llm.Message) string {
	s := "["
	for i, m := range msgs {
		if i > 0 {
			s += ", "
		}
		s += "{" + string(m.Role) + ":" + m.Content + "}"
	}
	return s + "]"
}

// ---- 便捷构造函数 ----

func TestSystemSlotConstructor(t *testing.T) {
	s := SystemSlot("persona", 0, "你是助手")
	if s.ID != "persona" || s.Position != System || s.Priority != 0 || s.Content != "你是助手" || !s.Enabled {
		t.Errorf("SystemSlot = %+v", s)
	}
}

func TestBeforeSlotConstructor(t *testing.T) {
	s := BeforeSlot("ctx", llm.System, 10, "背景")
	if s.ID != "ctx" || s.Role != llm.System || s.Position != Before || s.Priority != 10 || !s.Enabled {
		t.Errorf("BeforeSlot = %+v", s)
	}
}

func TestAfterSlotConstructor(t *testing.T) {
	s := AfterSlot("rag", llm.User, 0, "检索")
	if s.Position != After || s.Role != llm.User {
		t.Errorf("AfterSlot = %+v", s)
	}
}

func TestChatSlotConstructor(t *testing.T) {
	s := ChatSlot("inject", llm.System, 2, 5, "注入")
	if s.Position != Chat || s.Depth != 2 || s.Priority != 5 {
		t.Errorf("ChatSlot = %+v", s)
	}
}

func TestSlotChaining(t *testing.T) {
	s := SystemSlot("planner", 100, "ReAct").
		When(func(ctx Context) bool { return ctx.Step == 1 }).
		WithSource("planner").
		WithMaxTokens(500)

	if s.Condition == nil || s.Source != "planner" || s.MaxTokens != 500 {
		t.Errorf("chained slot = %+v", s)
	}
}

func TestDynamic(t *testing.T) {
	s := SystemSlot("sk", 50, "fallback").Dynamic(func(Context) string { return "动态" })

	asm := New(s)
	out := asm.Build(Context{}, llm.Request{})
	assertSystem(t, out.System, "动态")
}

func TestConstructorsInAssembler(t *testing.T) {
	asm := New(
		SystemSlot("persona", 0, "你是助手"),
		AfterSlot("guard", llm.System, 0, "护栏"),
	)
	out := asm.Build(Context{}, llm.Request{Messages: []llm.Message{msg(llm.User, "hi")}})

	assertSystem(t, out.System, "你是助手")
	assertMessages(t, out.Messages,
		msg(llm.System, "护栏"),
		msg(llm.User, "hi"),
	)
}

// ---- MaxTokens 截断 ----

func TestMaxTokens_Truncate(t *testing.T) {
	asm := New(
		SystemSlot("long", 0, "一二三四五六七八九十").WithMaxTokens(5),
	)
	out := asm.Build(Context{}, llm.Request{})
	// 默认 TokenCounter=RuneCount,截断到 5 个 rune
	assertSystem(t, out.System, "一二三四五")
}

func TestMaxTokens_NoTruncateWhenUnderLimit(t *testing.T) {
	asm := New(
		SystemSlot("short", 0, "abc").WithMaxTokens(100),
	)
	out := asm.Build(Context{}, llm.Request{})
	assertSystem(t, out.System, "abc")
}

func TestMaxTokens_Zero_Unlimited(t *testing.T) {
	content := "不截断的很长内容"
	asm := New(
		SystemSlot("x", 0, content), // MaxTokens 默认 0
	)
	out := asm.Build(Context{}, llm.Request{})
	assertSystem(t, out.System, content)
}

func TestMaxTokens_CustomCounter(t *testing.T) {
	// 自定义计数器:每个空格分隔的 word 算 1 token
	wordCount := func(s string) int {
		if s == "" {
			return 0
		}
		return len(strings.Fields(s))
	}
	asm := New(
		SystemSlot("words", 0, "one two three four five").WithMaxTokens(3),
	)
	asm.TokenCounter = wordCount
	out := asm.Build(Context{}, llm.Request{})
	// "one two three" = 3 words, "one two three four" = 4 words > 3
	// 二分查找应找到 "one two three" (rune 粒度下可能含尾部空格,但 Fields 计数不受影响)
	got := strings.TrimSpace(out.System)
	if wordCount(got) > 3 {
		t.Errorf("got %q (%d words), want ≤3 words", got, wordCount(got))
	}
	if !strings.HasPrefix("one two three four five", got) {
		t.Errorf("got %q, want prefix of original", got)
	}
}

func TestMaxTokens_OnMessageSlot(t *testing.T) {
	asm := New(
		AfterSlot("rag", llm.System, 0, "一二三四五六七八九十").WithMaxTokens(4),
	)
	out := asm.Build(Context{}, llm.Request{Messages: []llm.Message{msg(llm.User, "hi")}})
	assertMessages(t, out.Messages,
		msg(llm.System, "一二三四"),
		msg(llm.User, "hi"),
	)
}

// ---- SystemBudget ----

func TestSystemBudget_DropsLowPriority(t *testing.T) {
	asm := New(
		SystemSlot("high", 0, "AAAA"),  // 4 runes
		SystemSlot("mid", 50, "BBBB"),  // 4 runes
		SystemSlot("low", 100, "CCCC"), // 4 runes
	)
	// 总量 = 4 + 2("\n\n") + 4 + 2 + 4 = 16 runes
	// 预算 10: 丢弃 low → 4+2+4=10 ✓
	asm.SystemBudget = 10
	out := asm.Build(Context{}, llm.Request{})
	assertSystem(t, out.System, "AAAA\n\nBBBB")
}

func TestSystemBudget_DropsMultiple(t *testing.T) {
	asm := New(
		SystemSlot("a", 0, "AAA"),   // 3
		SystemSlot("b", 50, "BBB"),  // 3
		SystemSlot("c", 100, "CCC"), // 3
	)
	// 预算 3: 只能留 a
	asm.SystemBudget = 3
	out := asm.Build(Context{}, llm.Request{})
	assertSystem(t, out.System, "AAA")
}

func TestSystemBudget_Zero_Unlimited(t *testing.T) {
	asm := New(
		SystemSlot("a", 0, "A"),
		SystemSlot("b", 10, "B"),
	)
	// SystemBudget 默认 0 = 不限
	out := asm.Build(Context{}, llm.Request{})
	assertSystem(t, out.System, "A\n\nB")
}

func TestSystemBudget_DoesNotAffectMessages(t *testing.T) {
	asm := New(
		SystemSlot("sys", 0, "SYS"),
		AfterSlot("rag", llm.System, 0, "很长很长的RAG结果"),
	)
	asm.SystemBudget = 5
	out := asm.Build(Context{}, llm.Request{Messages: []llm.Message{msg(llm.User, "hi")}})
	assertSystem(t, out.System, "SYS")
	// message slot 不受 SystemBudget 影响
	assertMessages(t, out.Messages,
		msg(llm.System, "很长很长的RAG结果"),
		msg(llm.User, "hi"),
	)
}

func TestSystemBudget_WithFullControl(t *testing.T) {
	asm := New(
		SystemSlot("a", 0, "保留"),
		SystemSlot("b", 100, "丢弃"),
	)
	asm.FullControl = true
	asm.SystemBudget = 4 // "保留" = 2 runes, 在预算内; 加上 "丢弃" + sep = 2+2+2 = 6 > 4
	out := asm.Build(Context{}, llm.Request{System: "旧内容"})
	assertSystem(t, out.System, "保留")
}

// ---- Tests ----

func TestSystemSlots(t *testing.T) {
	asm := New(
		Slot{ID: "base", Position: System, Priority: 0, Content: "你是助手", Enabled: true},
		Slot{ID: "extra", Position: System, Priority: 10, Content: "请使用中文", Enabled: true},
	)
	req := llm.Request{System: "原始指令"}
	out := asm.Build(Context{}, req)

	assertSystem(t, out.System, "原始指令\n\n你是助手\n\n请使用中文")
}

func TestSystemPriorityOrder(t *testing.T) {
	asm := New(
		Slot{ID: "low", Position: System, Priority: 100, Content: "后", Enabled: true},
		Slot{ID: "high", Position: System, Priority: 0, Content: "先", Enabled: true},
	)
	out := asm.Build(Context{}, llm.Request{})

	assertSystem(t, out.System, "先\n\n后")
}

func TestBeforeSlots(t *testing.T) {
	asm := New(
		Slot{ID: "ctx", Role: llm.System, Position: Before, Priority: 0, Content: "背景信息", Enabled: true},
	)
	req := llm.Request{Messages: []llm.Message{msg(llm.User, "你好")}}
	out := asm.Build(Context{}, req)

	assertMessages(t, out.Messages,
		msg(llm.System, "背景信息"),
		msg(llm.User, "你好"),
	)
}

func TestAfterSlots_BeforeLastUser(t *testing.T) {
	asm := New(
		Slot{ID: "rag", Role: llm.System, Position: After, Priority: 0, Content: "检索结果:…", Enabled: true},
	)
	req := llm.Request{Messages: []llm.Message{
		msg(llm.User, "第一轮"),
		msg(llm.Assistant, "回复"),
		msg(llm.User, "第二轮"),
	}}
	out := asm.Build(Context{}, req)

	assertMessages(t, out.Messages,
		msg(llm.User, "第一轮"),
		msg(llm.Assistant, "回复"),
		msg(llm.System, "检索结果:…"),
		msg(llm.User, "第二轮"),
	)
}

func TestAfterSlots_NoUserMessage(t *testing.T) {
	asm := New(
		Slot{ID: "guard", Role: llm.System, Position: After, Priority: 0, Content: "护栏", Enabled: true},
	)
	req := llm.Request{Messages: []llm.Message{msg(llm.Assistant, "嗨")}}
	out := asm.Build(Context{}, req)

	assertMessages(t, out.Messages,
		msg(llm.Assistant, "嗨"),
		msg(llm.System, "护栏"),
	)
}

func TestChatDepth(t *testing.T) {
	asm := New(
		Slot{ID: "inject", Role: llm.System, Position: Chat, Depth: 1, Priority: 0, Content: "注入", Enabled: true},
	)
	req := llm.Request{Messages: []llm.Message{
		msg(llm.User, "a"),
		msg(llm.Assistant, "b"),
		msg(llm.User, "c"),
	}}
	out := asm.Build(Context{}, req)

	// Depth=1: 在倒数第1条之前插入
	assertMessages(t, out.Messages,
		msg(llm.User, "a"),
		msg(llm.Assistant, "b"),
		msg(llm.System, "注入"),
		msg(llm.User, "c"),
	)
}

func TestChatDepthZero(t *testing.T) {
	asm := New(
		Slot{ID: "tail", Role: llm.User, Position: Chat, Depth: 0, Priority: 0, Content: "追加", Enabled: true},
	)
	req := llm.Request{Messages: []llm.Message{msg(llm.User, "a")}}
	out := asm.Build(Context{}, req)

	assertMessages(t, out.Messages,
		msg(llm.User, "a"),
		msg(llm.User, "追加"),
	)
}

func TestChatSameDepthPriority(t *testing.T) {
	asm := New(
		Slot{ID: "b", Role: llm.System, Position: Chat, Depth: 1, Priority: 10, Content: "B", Enabled: true},
		Slot{ID: "a", Role: llm.System, Position: Chat, Depth: 1, Priority: 0, Content: "A", Enabled: true},
	)
	req := llm.Request{Messages: []llm.Message{
		msg(llm.User, "m0"),
		msg(llm.User, "m1"),
	}}
	out := asm.Build(Context{}, req)

	// 同 depth=1,priority 低的在前
	assertMessages(t, out.Messages,
		msg(llm.User, "m0"),
		msg(llm.System, "A"),
		msg(llm.System, "B"),
		msg(llm.User, "m1"),
	)
}

func TestChatMultipleDepths(t *testing.T) {
	asm := New(
		Slot{ID: "shallow", Role: llm.System, Position: Chat, Depth: 0, Priority: 0, Content: "浅", Enabled: true},
		Slot{ID: "deep", Role: llm.System, Position: Chat, Depth: 2, Priority: 0, Content: "深", Enabled: true},
	)
	req := llm.Request{Messages: []llm.Message{
		msg(llm.User, "m0"),
		msg(llm.Assistant, "m1"),
		msg(llm.User, "m2"),
	}}
	out := asm.Build(Context{}, req)

	// deep at depth=2 → before m1; shallow at depth=0 → append
	assertMessages(t, out.Messages,
		msg(llm.User, "m0"),
		msg(llm.System, "深"),
		msg(llm.Assistant, "m1"),
		msg(llm.User, "m2"),
		msg(llm.System, "浅"),
	)
}

func TestDisabledSlot(t *testing.T) {
	asm := New(
		Slot{ID: "off", Position: System, Content: "不应出现", Enabled: false},
		Slot{ID: "on", Position: System, Content: "应出现", Enabled: true},
	)
	out := asm.Build(Context{}, llm.Request{})

	assertSystem(t, out.System, "应出现")
}

func TestCondition(t *testing.T) {
	asm := New(
		Slot{
			ID: "first_only", Position: System, Priority: 0,
			Content: "仅首轮", Enabled: true,
			Condition: func(ctx Context) bool { return ctx.Step == 1 },
		},
	)

	out1 := asm.Build(Context{Step: 1}, llm.Request{})
	assertSystem(t, out1.System, "仅首轮")

	out2 := asm.Build(Context{Step: 2}, llm.Request{})
	assertSystem(t, out2.System, "")
}

func TestContentFunc(t *testing.T) {
	calls := 0
	asm := New(
		Slot{
			ID: "dynamic", Position: System, Priority: 0, Enabled: true,
			ContentFunc: func(ctx Context) string {
				calls++
				return "动态内容"
			},
		},
	)
	out := asm.Build(Context{}, llm.Request{})

	assertSystem(t, out.System, "动态内容")
	if calls != 1 {
		t.Errorf("ContentFunc called %d times, want 1", calls)
	}
}

func TestContentFuncOverridesContent(t *testing.T) {
	asm := New(
		Slot{
			ID: "both", Position: System, Priority: 0, Enabled: true,
			Content:     "静态",
			ContentFunc: func(Context) string { return "动态" },
		},
	)
	out := asm.Build(Context{}, llm.Request{})

	assertSystem(t, out.System, "动态")
}

func TestEmptyContentSkipped(t *testing.T) {
	asm := New(
		Slot{ID: "empty", Position: System, Content: "", Enabled: true},
		Slot{ID: "nonempty", Position: System, Content: "有内容", Enabled: true},
	)
	out := asm.Build(Context{}, llm.Request{})

	assertSystem(t, out.System, "有内容")
}

func TestEmptyContentFuncSkipped(t *testing.T) {
	asm := New(
		Slot{ID: "empty_fn", Position: System, Enabled: true, ContentFunc: func(Context) string { return "" }},
		Slot{ID: "ok", Position: System, Content: "OK", Enabled: true},
	)
	out := asm.Build(Context{}, llm.Request{})

	assertSystem(t, out.System, "OK")
}

func TestNoSlots(t *testing.T) {
	asm := New()
	req := llm.Request{System: "保持", Messages: []llm.Message{msg(llm.User, "hi")}}
	out := asm.Build(Context{}, req)

	assertSystem(t, out.System, "保持")
	assertMessages(t, out.Messages, msg(llm.User, "hi"))
}

func TestOriginalRequestUnmodified(t *testing.T) {
	asm := New(
		Slot{ID: "s", Position: System, Content: "追加", Enabled: true},
		Slot{ID: "b", Role: llm.System, Position: Before, Content: "前置", Enabled: true},
	)
	orig := llm.Request{
		System:   "原始",
		Messages: []llm.Message{msg(llm.User, "hi")},
	}
	origMsgs := make([]llm.Message, len(orig.Messages))
	copy(origMsgs, orig.Messages)

	_ = asm.Build(Context{}, orig)

	if orig.System != "原始" {
		t.Error("original System was modified")
	}
	assertMessages(t, orig.Messages, origMsgs...)
}

func TestRemove(t *testing.T) {
	asm := New(
		Slot{ID: "a", Position: System, Content: "A", Enabled: true},
		Slot{ID: "b", Position: System, Content: "B", Enabled: true},
	)
	asm.Remove("a")
	out := asm.Build(Context{}, llm.Request{})

	assertSystem(t, out.System, "B")
}

func TestSetEnabled(t *testing.T) {
	asm := New(
		Slot{ID: "x", Position: System, Content: "X", Enabled: true},
	)
	asm.SetEnabled("x", false)
	out := asm.Build(Context{}, llm.Request{})

	assertSystem(t, out.System, "")
}

func TestSetContent(t *testing.T) {
	asm := New(
		Slot{ID: "c", Position: System, Content: "旧", Enabled: true},
	)
	asm.SetContent("c", "新")
	out := asm.Build(Context{}, llm.Request{})

	assertSystem(t, out.System, "新")
}

func TestAdd(t *testing.T) {
	asm := New()
	asm.Add(Slot{ID: "late", Position: System, Content: "后加", Enabled: true})
	out := asm.Build(Context{}, llm.Request{})

	assertSystem(t, out.System, "后加")
}

func TestSnapshot(t *testing.T) {
	asm := New(
		Slot{ID: "a", Position: System, Priority: 0, Content: "A", Enabled: true, Source: "test"},
		Slot{ID: "b", Position: System, Priority: 10, Content: "", Enabled: true},
		Slot{ID: "c", Position: Before, Priority: 0, Content: "C", Enabled: false},
	)
	snap := asm.Snapshot(Context{})

	if len(snap) != 1 {
		t.Fatalf("Snapshot: len=%d, want 1", len(snap))
	}
	if snap[0].ID != "a" || snap[0].Source != "test" || snap[0].Content != "A" {
		t.Errorf("Snapshot[0] = %+v", snap[0])
	}
}

func TestFullAssembly(t *testing.T) {
	asm := New(
		Slot{ID: "persona", Position: System, Priority: 0, Content: "你是助手", Enabled: true},
		Slot{ID: "skills", Position: System, Priority: 50, Content: "<skills>…</skills>", Enabled: true},
		Slot{ID: "planner", Position: System, Priority: 100, Content: "请按 ReAct 方式",
			Enabled: true, Condition: func(ctx Context) bool { return ctx.Step == 1 }},
		Slot{ID: "summary", Role: llm.System, Position: Before, Priority: 0, Content: "摘要:用户在讨论XX", Enabled: true},
		Slot{ID: "rag", Role: llm.System, Position: After, Priority: 0, Content: "检索到:文档A", Enabled: true},
		Slot{ID: "guardrail", Role: llm.System, Position: After, Priority: 10, Content: "不要泄露密码", Enabled: true},
	)

	req := llm.Request{
		System: "base",
		Messages: []llm.Message{
			msg(llm.User, "第一轮"),
			msg(llm.Assistant, "回复"),
			msg(llm.User, "第二轮问题"),
		},
	}
	out := asm.Build(Context{Step: 1, MessageCount: 3}, req)

	assertSystem(t, out.System, "base\n\n你是助手\n\n<skills>…</skills>\n\n请按 ReAct 方式")
	assertMessages(t, out.Messages,
		msg(llm.System, "摘要:用户在讨论XX"),
		msg(llm.User, "第一轮"),
		msg(llm.Assistant, "回复"),
		msg(llm.System, "检索到:文档A"),
		msg(llm.System, "不要泄露密码"),
		msg(llm.User, "第二轮问题"),
	)
}

func TestFullAssembly_Step2NoPlannerSlot(t *testing.T) {
	asm := New(
		Slot{ID: "persona", Position: System, Priority: 0, Content: "你是助手", Enabled: true},
		Slot{ID: "planner", Position: System, Priority: 100, Content: "请按 ReAct 方式",
			Enabled: true, Condition: func(ctx Context) bool { return ctx.Step == 1 }},
	)

	out := asm.Build(Context{Step: 2}, llm.Request{System: "base"})
	assertSystem(t, out.System, "base\n\n你是助手")
}

func TestHook(t *testing.T) {
	asm := New(
		Slot{ID: "h", Position: System, Content: "hook内容", Enabled: true},
	)
	hook := asm.Hook()

	req := llm.Request{Messages: []llm.Message{msg(llm.User, "hi")}}
	if err := hook(nil, 1, &req); err != nil {
		t.Fatal(err)
	}
	assertSystem(t, req.System, "hook内容")
}

// ---- FullControl 模式 ----

func TestFullControl_ReplacesSystem(t *testing.T) {
	asm := New(
		Slot{ID: "persona", Position: System, Priority: 0, Content: "你是助手", Enabled: true},
		Slot{ID: "planner", Position: System, Priority: 100, Content: "ReAct指令", Enabled: true},
	)
	asm.FullControl = true

	// req.System 模拟 Session/Planner 已注入的内容——FullControl 会丢弃它
	req := llm.Request{System: "Session注入的摘要\n\nPlanner注入的指令"}
	out := asm.Build(Context{}, req)

	assertSystem(t, out.System, "你是助手\n\nReAct指令")
}

func TestFullControl_NoSlots_ClearsSystem(t *testing.T) {
	asm := New()
	asm.FullControl = true

	req := llm.Request{System: "会被清除"}
	out := asm.Build(Context{}, req)

	assertSystem(t, out.System, "")
}

func TestFullControl_DisabledSlots_ClearsSystem(t *testing.T) {
	asm := New(
		Slot{ID: "off", Position: System, Content: "禁用的", Enabled: false},
	)
	asm.FullControl = true

	req := llm.Request{System: "会被清除"}
	out := asm.Build(Context{}, req)

	assertSystem(t, out.System, "")
}

func TestIncremental_PreservesExistingSystem(t *testing.T) {
	asm := New(
		Slot{ID: "extra", Position: System, Priority: 0, Content: "追加", Enabled: true},
	)
	// FullControl 默认 false

	req := llm.Request{System: "已有内容"}
	out := asm.Build(Context{}, req)

	assertSystem(t, out.System, "已有内容\n\n追加")
}

// ---- ChainHooks ----

func TestChainHooks(t *testing.T) {
	var order []string

	h1 := func(_ context.Context, _ int, req *llm.Request) error {
		order = append(order, "h1")
		req.System += "[h1]"
		return nil
	}
	h2 := func(_ context.Context, _ int, req *llm.Request) error {
		order = append(order, "h2")
		req.System += "[h2]"
		return nil
	}

	chain := ChainHooks(h1, nil, h2) // nil 应被跳过
	req := llm.Request{}
	if err := chain(context.Background(), 1, &req); err != nil {
		t.Fatal(err)
	}

	assertSystem(t, req.System, "[h1][h2]")
	if len(order) != 2 || order[0] != "h1" || order[1] != "h2" {
		t.Errorf("order = %v, want [h1 h2]", order)
	}
}

func TestChainHooks_ErrorStops(t *testing.T) {
	errBoom := errString("boom")
	h1 := func(context.Context, int, *llm.Request) error { return errBoom }
	h2 := func(context.Context, int, *llm.Request) error {
		t.Fatal("h2 should not be called")
		return nil
	}

	chain := ChainHooks(h1, h2)
	err := chain(context.Background(), 1, &llm.Request{})
	if err == nil || err.Error() != "boom" {
		t.Errorf("err = %v, want boom", err)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestChainHooks_WithAssembler(t *testing.T) {
	asm := New(
		Slot{ID: "s", Position: System, Content: "slot内容", Enabled: true},
	)
	asm.FullControl = true

	logged := false
	logger := func(_ context.Context, _ int, _ *llm.Request) error {
		logged = true
		return nil
	}

	chain := ChainHooks(logger, asm.Hook())
	req := llm.Request{System: "旧"}
	if err := chain(context.Background(), 1, &req); err != nil {
		t.Fatal(err)
	}

	if !logged {
		t.Error("logger hook was not called")
	}
	assertSystem(t, req.System, "slot内容")
}

// ---- 其余测试 ----

func TestPositionString(t *testing.T) {
	cases := []struct {
		p    Position
		want string
	}{
		{System, "system"},
		{Before, "before"},
		{After, "after"},
		{Chat, "chat"},
		{Position(99), "unknown"},
	}
	for _, c := range cases {
		if got := c.p.String(); got != c.want {
			t.Errorf("Position(%d).String() = %q, want %q", c.p, got, c.want)
		}
	}
}

func TestChatDepthExceedsLength(t *testing.T) {
	asm := New(
		Slot{ID: "deep", Role: llm.System, Position: Chat, Depth: 100, Priority: 0, Content: "很深", Enabled: true},
	)
	req := llm.Request{Messages: []llm.Message{msg(llm.User, "唯一")}}
	out := asm.Build(Context{}, req)

	// depth 超出长度时 clamp 到 0
	assertMessages(t, out.Messages,
		msg(llm.System, "很深"),
		msg(llm.User, "唯一"),
	)
}

func TestMultipleAfterSlotsPreserveOrder(t *testing.T) {
	asm := New(
		Slot{ID: "a2", Role: llm.System, Position: After, Priority: 10, Content: "第二", Enabled: true},
		Slot{ID: "a1", Role: llm.System, Position: After, Priority: 0, Content: "第一", Enabled: true},
	)
	req := llm.Request{Messages: []llm.Message{
		msg(llm.Assistant, "历史"),
		msg(llm.User, "当前"),
	}}
	out := asm.Build(Context{}, req)

	assertMessages(t, out.Messages,
		msg(llm.Assistant, "历史"),
		msg(llm.System, "第一"),
		msg(llm.System, "第二"),
		msg(llm.User, "当前"),
	)
}
