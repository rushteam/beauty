package session_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
	"github.com/rushteam/beauty/contrib/llm/agent/session"
)

// replyClient 是每次都回固定文本的假 client(并记录最后一次请求)。
type replyClient struct {
	reply string
	last  llm.Request
}

func (c *replyClient) Generate(_ context.Context, req llm.Request) (*llm.Response, error) {
	c.last = req
	return &llm.Response{Content: c.reply, Model: req.Model}, nil
}
func (c *replyClient) Stream(context.Context, llm.Request) (<-chan llm.Chunk, error) {
	return nil, errors.New("unused")
}

func user(text string) llm.Request {
	return llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.User, Content: text}}}
}

func TestMemoryStore_RoundTrip(t *testing.T) {
	st := session.NewMemoryStore()
	if s, _ := st.Load(context.Background(), "x"); s != nil {
		t.Fatal("不存在的会话应返回 nil")
	}
	_ = st.Save(context.Background(), &session.Session{ID: "x", Summary: "s", Messages: []llm.Message{{Role: llm.User, Content: "hi"}}})
	got, _ := st.Load(context.Background(), "x")
	if got == nil || got.Summary != "s" || len(got.Messages) != 1 {
		t.Fatalf("load = %+v", got)
	}
	// 存的是深拷贝:改动返回值不影响存储。
	got.Messages[0].Content = "changed"
	again, _ := st.Load(context.Background(), "x")
	if again.Messages[0].Content != "hi" {
		t.Fatal("Store 应存深拷贝,不与调用方共享底层")
	}
}

// 多轮:第二轮请求应带上第一轮的 user+assistant 历史。
func TestManager_PersistsAndInjectsHistory(t *testing.T) {
	ctx := context.Background()
	fc := &replyClient{reply: "回复A"}
	r := &agent.Runner{Client: fc}
	m := &session.Manager{Store: session.NewMemoryStore()}

	if out := m.Run(ctx, "s1", r, user("第一句")); !out.IsDone() {
		if _, err := out.Final(); err != nil {
			t.Fatalf("run1: %v", err)
		}
	}
	fc.reply = "回复B"
	if out := m.Run(ctx, "s1", r, user("第二句")); !out.IsDone() {
		if _, err := out.Final(); err != nil {
			t.Fatalf("run2: %v", err)
		}
	}

	// 第二轮 Runner 收到的消息:第一句(user)、回复A(assistant)、第二句(user)。
	msgs := fc.last.Messages
	if len(msgs) != 3 {
		t.Fatalf("第二轮应含历史,消息数=%d: %+v", len(msgs), msgs)
	}
	if msgs[0].Content != "第一句" || msgs[1].Content != "回复A" || msgs[2].Content != "第二句" {
		t.Fatalf("历史拼接不对: %+v", msgs)
	}
}

// Manager 现在接受任意 agent.Agent:非 *Runner 的组合体(这里 VerifyLoop 包 Runner)也能套会话记忆,
// 走 runAgent 的非 Runner 分支,历史依旧正确拼接与持久化。
func TestManager_AcceptsNonRunnerAgent(t *testing.T) {
	ctx := context.Background()
	fc := &replyClient{reply: "答复A"}
	a := &agent.VerifyLoop{Agent: &agent.Runner{Client: fc}} // 实现 Agent,但不是 *Runner
	m := &session.Manager{Store: session.NewMemoryStore()}

	if out := m.Run(ctx, "s1", a, user("第一句")); !out.IsDone() {
		if _, err := out.Final(); err != nil {
			t.Fatalf("run1: %v", err)
		}
	}
	fc.reply = "答复B"
	if out := m.Run(ctx, "s1", a, user("第二句")); !out.IsDone() {
		if _, err := out.Final(); err != nil {
			t.Fatalf("run2: %v", err)
		}
	}
	msgs := fc.last.Messages
	if len(msgs) != 3 || msgs[0].Content != "第一句" || msgs[1].Content != "答复A" || msgs[2].Content != "第二句" {
		t.Fatalf("非 Runner Agent 的历史拼接不对: %+v", msgs)
	}
}

func TestManager_RunStream_Persists(t *testing.T) {
	ctx := context.Background()
	fc := &replyClient{reply: "流式回复"}
	r := &agent.Runner{Client: fc}
	m := &session.Manager{Store: session.NewMemoryStore()}

	var final string
	for ev := range m.RunStream(ctx, "s-stream", r, user("你好")) {
		if ev.Type == agent.EventError {
			t.Fatal(ev.Err)
		}
		if ev.Type == agent.EventFinal {
			final = ev.Response.Content
		}
	}
	if final != "流式回复" {
		t.Fatalf("final=%q", final)
	}
	sess, err := m.Store.Load(ctx, "s-stream")
	if err != nil || sess == nil {
		t.Fatalf("load: %v %v", err, sess)
	}
	if len(sess.Messages) != 2 || sess.Messages[0].Content != "你好" || sess.Messages[1].Content != "流式回复" {
		t.Fatalf("sess=%+v", sess)
	}
}

// 摘要触发:超阈值后早期消息折叠成 Summary,只保留最近若干条,且下轮作为系统背景注入。
func TestManager_RollingSummary(t *testing.T) {
	ctx := context.Background()
	runClient := &replyClient{reply: "ok"}
	sumClient := &replyClient{reply: "这是摘要"}
	r := &agent.Runner{Client: runClient}
	st := session.NewMemoryStore()
	m := &session.Manager{
		Store:      st,
		Summarizer: &session.Summarizer{Client: sumClient, Model: "m", MaxMessages: 4, KeepRecent: 2},
	}

	// 跑 3 轮 → 每轮 +2 条(user+assistant)= 6 条 > MaxMessages(4),触发摘要。
	for _, in := range []string{"a", "b", "c"} {
		if out := m.Run(ctx, "s", r, user(in)); !out.IsDone() {
			if _, err := out.Final(); err != nil {
				t.Fatalf("run %s: %v", in, err)
			}
		}
	}
	sess, _ := st.Load(ctx, "s")
	if sess.Summary != "这是摘要" {
		t.Fatalf("应生成摘要, got %q", sess.Summary)
	}
	if len(sess.Messages) != 2 {
		t.Fatalf("应只保留最近 2 条, got %d", len(sess.Messages))
	}
	// 摘要器确实收到了被折叠的历史。
	if !strings.Contains(sumClient.last.Messages[0].Content, "a") {
		t.Fatalf("摘要输入应含早期消息: %q", sumClient.last.Messages[0].Content)
	}

	// 下一轮:摘要作为系统背景注入。
	_ = m.Run(ctx, "s", r, user("d"))
	if !strings.Contains(runClient.last.System, "这是摘要") {
		t.Fatalf("下一轮应把摘要注入 System: %q", runClient.last.System)
	}
}
