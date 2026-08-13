// Package session 给 llm/agent 加"会话记忆":把多轮对话历史持久化,并在超长时滚动摘要。
// Manager 是 Runner 之上的薄编排——加载历史 → 拼进请求(旧摘要作为系统上下文 + 历史消息) →
// 跑 Runner → 回写本轮 user/assistant → 按需摘要 → 保存。
//
// 边界(机制而非策略):存哪(内存/文件/sqldb/redis)由 Store 决定;何时摘要、保留多少条、摘要用哪个
// 模型都可配。本包只做接口 + MemoryStore + FileStore(JSON 落盘) + 编排,纯标准库,零外部依赖。
package session

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
)

// Session 是一段对话的持久状态:滚动摘要 + 近期消息。
type Session struct {
	ID           string
	Summary      string // 早期消息被折叠成的摘要(可为空)
	Messages     []llm.Message
	PendingRunID string // 非空表示有暂停中的 agent run,应走 Manager.Continue
	UpdatedAt    time.Time
}

// Store 持久化会话快照。Load 对不存在的 id 应返回 (nil, nil) 而非错误。
type Store interface {
	Load(ctx context.Context, id string) (*Session, error)
	Save(ctx context.Context, s *Session) error
}

// EventStore 持久化 append-only 事件日志(event-sourcing)。
// 与 Store 正交——只需要快照的场景无需实现此接口;
// 需要事件溯源(Replay/Fork)时,Store 实现可同时实现 EventStore。
type EventStore interface {
	// AppendEvents 追加事件到指定会话的事件日志。
	AppendEvents(ctx context.Context, sessionID string, events ...SessionEvent) error
	// LoadEvents 读取指定会话的全部事件(有序)。不存在时返回 (nil, nil)。
	LoadEvents(ctx context.Context, sessionID string) ([]SessionEvent, error)
}

// MemoryStore 是并发安全的内存 Store(测试/单机用;生产可在 contrib 实现 sqldb/redis 版)。
type MemoryStore struct {
	mu     sync.RWMutex
	m      map[string]*Session
	events map[string][]SessionEvent
}

// NewMemoryStore 创建内存 Store。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		m:      map[string]*Session{},
		events: map[string][]SessionEvent{},
	}
}

// Load 返回会话的深拷贝(避免调用方与存储共享底层切片)。
func (s *MemoryStore) Load(ctx context.Context, id string) (*Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.m[id]
	if !ok {
		return nil, nil
	}
	return cloneSession(sess), nil
}

// Save 存入会话的深拷贝。
func (s *MemoryStore) Save(ctx context.Context, sess *Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[sess.ID] = cloneSession(sess)
	return nil
}

func (s *MemoryStore) AppendEvents(ctx context.Context, sessionID string, events ...SessionEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for i := range events {
		if events[i].Timestamp.IsZero() {
			events[i].Timestamp = now
		}
	}
	s.events[sessionID] = append(s.events[sessionID], events...)
	return nil
}

func (s *MemoryStore) LoadEvents(ctx context.Context, sessionID string) ([]SessionEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	evts, ok := s.events[sessionID]
	if !ok {
		return nil, nil
	}
	out := make([]SessionEvent, len(evts))
	copy(out, evts)
	return out, nil
}

var (
	_ Store      = (*MemoryStore)(nil)
	_ EventStore = (*MemoryStore)(nil)
)

func cloneSession(s *Session) *Session {
	msgs := make([]llm.Message, len(s.Messages))
	copy(msgs, s.Messages)
	return &Session{ID: s.ID, Summary: s.Summary, Messages: msgs, PendingRunID: s.PendingRunID, UpdatedAt: s.UpdatedAt}
}

// Manager 用 Store(+ 可选 Summarizer)给 Runner 加会话记忆。
type Manager struct {
	Store      Store
	Summarizer *Summarizer // 为 nil 则不摘要(历史会一直增长)
}

// prepare 加载会话并拼出完整 Request;返回会话、本轮新消息、拼好的请求。
func (m *Manager) prepare(ctx context.Context, id string, req llm.Request) (*Session, []llm.Message, llm.Request, error) {
	sess, err := m.Store.Load(ctx, id)
	if err != nil {
		return nil, nil, req, err
	}
	if sess == nil {
		sess = &Session{ID: id}
	}
	newMsgs := req.Messages
	full := req
	if sess.Summary != "" {
		full.System = joinSystem(req.System, "以下是此前对话的摘要,作为背景:\n"+sess.Summary)
	}
	full.Messages = append(append([]llm.Message{}, sess.Messages...), newMsgs...)
	return sess, newMsgs, full, nil
}

// persist 把本轮 user 输入与最终 assistant 回复写入会话并保存。
// intermediates 包含 agent 循环中的中间消息(assistant tool_calls + tool results)。
func (m *Manager) persist(ctx context.Context, sess *Session, newMsgs []llm.Message, intermediates []llm.Message, assistant string) error {
	sess.Messages = append(sess.Messages, newMsgs...)
	sess.Messages = append(sess.Messages, intermediates...)
	sess.Messages = append(sess.Messages, llm.Message{Role: llm.Assistant, Content: assistant})
	if m.Summarizer != nil {
		if err := m.Summarizer.compress(ctx, sess); err != nil {
			return err
		}
	}
	sess.UpdatedAt = time.Now()
	return m.Store.Save(ctx, sess)
}

// Run 以会话 id 跑一轮:req.Messages 只放**本轮新输入**。
// 返回 RunOutcome:Paused 时写入 PendingRunID 且不 persist 终态;Done 时追加消息。
func (m *Manager) Run(ctx context.Context, id string, a agent.Agent, req llm.Request) agent.RunOutcome {
	sess, newMsgs, full, err := m.prepare(ctx, id, req)
	if err != nil {
		return agent.RunOutcome{Status: agent.StatusError, Err: err}
	}
	var intermediates []llm.Message
	out := m.runAgent(ctx, a, full, &intermediates)
	switch out.Status {
	case agent.StatusPaused:
		sess.PendingRunID = out.RunID
		sess.UpdatedAt = time.Now()
		if err := m.Store.Save(ctx, sess); err != nil {
			out.Status = agent.StatusError
			out.Err = err
		}
		return out
	case agent.StatusDone:
		sess.PendingRunID = ""
		content := ""
		if out.Response != nil {
			content = out.Response.Content
		}
		// 优先用 Outcome.Messages 中相对本轮新增的中间态;否则用 OnStep 捕获。
		if err := m.persist(ctx, sess, newMsgs, intermediates, content); err != nil {
			out.Status = agent.StatusError
			out.Err = err
		}
		return out
	default:
		return out
	}
}

// Continue 恢复会话上暂停的 run,完成后写入会话。
func (m *Manager) Continue(ctx context.Context, id string, a agent.Agent, resolutions []agent.Resolution) agent.RunOutcome {
	sess, err := m.Store.Load(ctx, id)
	if err != nil {
		return agent.RunOutcome{Status: agent.StatusError, Err: err}
	}
	if sess == nil || sess.PendingRunID == "" {
		return agent.RunOutcome{Status: agent.StatusError, Err: errNoPending}
	}
	out := a.Continue(ctx, sess.PendingRunID, resolutions)
	switch out.Status {
	case agent.StatusPaused:
		sess.PendingRunID = out.RunID
		sess.UpdatedAt = time.Now()
		_ = m.Store.Save(ctx, sess)
		return out
	case agent.StatusDone:
		sess.PendingRunID = ""
		content := ""
		if out.Response != nil {
			content = out.Response.Content
		}
		// Continue 路径没有本轮 newMsgs;只追加最终 assistant(中间态已在 agent RunStore)。
		if err := m.persist(ctx, sess, nil, nil, content); err != nil {
			out.Status = agent.StatusError
			out.Err = err
		}
		return out
	default:
		return out
	}
}

var errNoPending = errString("session: no pending paused run")

type errString string

func (e errString) Error() string { return string(e) }

// runAgent 跑底层 agent;若是 *Runner 则临时挂 OnStep 捕获工具调用中间消息。
func (m *Manager) runAgent(ctx context.Context, a agent.Agent, full llm.Request, intermediates *[]llm.Message) agent.RunOutcome {
	r, ok := a.(*agent.Runner)
	if !ok {
		return a.Run(ctx, full)
	}
	origOnStep := r.OnStep
	r.OnStep = func(step int, resp *llm.Response) {
		if len(resp.ToolCalls) > 0 {
			*intermediates = append(*intermediates, llm.Message{Role: llm.Assistant, Content: resp.Content, ToolCalls: resp.ToolCalls})
		}
		if origOnStep != nil {
			origOnStep(step, resp)
		}
	}
	defer func() { r.OnStep = origOnStep }()
	return r.Run(ctx, full)
}

// RunStream 与 Run 相同的会话编排,但转发底层 StreamAgent 的事件。
// EventFinal 时持久化;EventPaused 时记 PendingRunID;EventError 不写终态。
func (m *Manager) RunStream(ctx context.Context, id string, r agent.StreamAgent, req llm.Request) <-chan agent.Event {
	ch := make(chan agent.Event, 32)
	go func() {
		defer close(ch)
		emit := func(e agent.Event) { ch <- e }

		sess, newMsgs, full, err := m.prepare(ctx, id, req)
		if err != nil {
			emit(agent.Event{Type: agent.EventError, Err: err})
			return
		}
		var final *llm.Response
		var intermediates []llm.Message
		for ev := range r.RunStream(ctx, full) {
			switch ev.Type {
			case agent.EventFinal:
				final = ev.Response
				continue
			case agent.EventPaused:
				sess.PendingRunID = ev.RunID
				sess.UpdatedAt = time.Now()
				_ = m.Store.Save(ctx, sess)
				emit(ev)
				return
			case agent.EventStep:
				if ev.Response != nil && len(ev.Response.ToolCalls) > 0 {
					intermediates = append(intermediates, llm.Message{Role: llm.Assistant, Content: ev.Response.Content, ToolCalls: ev.Response.ToolCalls})
				}
			case agent.EventToolResult:
				if ev.ToolCall != nil {
					intermediates = append(intermediates, llm.Message{Role: llm.Tool, ToolCallID: ev.ToolCall.ID, Content: ev.Result})
				}
			}
			emit(ev)
			if ev.Type == agent.EventError {
				return
			}
		}
		if final == nil {
			return
		}
		sess.PendingRunID = ""
		if err := m.persist(ctx, sess, newMsgs, intermediates, final.Content); err != nil {
			emit(agent.Event{Type: agent.EventError, Response: final, Err: err})
			return
		}
		emit(agent.Event{Type: agent.EventFinal, Response: final})
	}()
	return ch
}

func joinSystem(base, extra string) string {
	if base == "" {
		return extra
	}
	return base + "\n\n" + extra
}

// Summarizer 在会话消息数超过 MaxMessages 时,把最早的一批折叠进 Summary,只保留最近 KeepRecent 条。
type Summarizer struct {
	Client      llm.Client
	Model       string
	MaxMessages int // 超过则触发摘要(<=0 用 20)
	KeepRecent  int // 保留最近多少条不摘要(<=0 用 6)
}

// compress 按阈值滚动摘要。未触发则原样返回。
func (s *Summarizer) compress(ctx context.Context, sess *Session) error {
	max, keep := s.MaxMessages, s.KeepRecent
	if max <= 0 {
		max = 20
	}
	if keep <= 0 {
		keep = 6
	}
	if len(sess.Messages) <= max {
		return nil
	}
	cut := len(sess.Messages) - keep
	older := sess.Messages[:cut]

	var b strings.Builder
	if sess.Summary != "" {
		b.WriteString("已有摘要:\n")
		b.WriteString(sess.Summary)
		b.WriteString("\n\n新增对话:\n")
	}
	for _, msg := range older {
		if msg.Content == "" {
			continue
		}
		b.WriteString(string(msg.Role))
		b.WriteString(": ")
		b.WriteString(msg.Content)
		b.WriteByte('\n')
	}

	resp, err := s.Client.Generate(ctx, llm.Request{
		Model:    s.Model,
		System:   "你是对话摘要器。把下面的对话压缩成简洁、信息完整的中文摘要,保留关键事实、决定与未决事项。只输出摘要本身。",
		Messages: []llm.Message{{Role: llm.User, Content: b.String()}},
	})
	if err != nil {
		return err
	}
	sess.Summary = strings.TrimSpace(resp.Content)
	sess.Messages = append([]llm.Message{}, sess.Messages[cut:]...)
	return nil
}
