// Package session 给 llm/agent 加"会话记忆":把多轮对话历史持久化,并在超长时滚动摘要。
package session

import (
	"context"
	"encoding/json"
	"iter"
	"strings"
	"sync"
	"time"

	"github.com/rushteam/beauty/contrib/llm"
	"github.com/rushteam/beauty/contrib/llm/agent"
	"github.com/rushteam/beauty/contrib/llm/agent/compaction"
)

// Session 是一段对话的持久状态:滚动摘要 + 近期消息。
type Session struct {
	ID           string
	Summary      string
	Messages     []llm.Message
	PendingRunID string
	UpdatedAt    time.Time
	// Usage 是各次顶层 run 的 token 合计。续跑时按 run 改写,不从被截断的消息回推。
	Usage llm.Usage `json:"usage,omitempty"`
	// Runs 是按发生顺序记下的 run。子 run 的 ParentID 指向顶层 run,分页时不占名额。
	Runs []Run `json:"runs,omitempty"`
	// Metadata 是中间件/harness 可在 session 中存储的任意 JSON 可序列化状态。
	// key 是命名空间字符串(如 "compaction", "approval_rules", "todo"),
	// value 是 json.RawMessage 以支持延迟反序列化。
	Metadata map[string]json.RawMessage `json:"metadata,omitempty"`
}

// GetMeta 从 session metadata 中反序列化指定 key 的值到 target。
// key 不存在时返回 false。
func (s *Session) GetMeta(key string, target any) (bool, error) {
	if s.Metadata == nil {
		return false, nil
	}
	raw, ok := s.Metadata[key]
	if !ok {
		return false, nil
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return false, err
	}
	return true, nil
}

// SetMeta 把 value 序列化后存入 session metadata。
func (s *Session) SetMeta(key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if s.Metadata == nil {
		s.Metadata = make(map[string]json.RawMessage)
	}
	s.Metadata[key] = raw
	return nil
}

// DeleteMeta 删除指定 key 的 metadata。
func (s *Session) DeleteMeta(key string) {
	if s.Metadata == nil {
		return
	}
	delete(s.Metadata, key)
}

// Store 持久化会话快照。Load 对不存在的 id 应返回 (nil, nil) 而非错误。
type Store interface {
	Load(ctx context.Context, id string) (*Session, error)
	Save(ctx context.Context, s *Session) error
}

// EventStore 持久化 append-only 事件日志(event-sourcing)。
type EventStore interface {
	AppendEvents(ctx context.Context, sessionID string, events ...SessionEvent) error
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
	var meta map[string]json.RawMessage
	if len(s.Metadata) > 0 {
		meta = make(map[string]json.RawMessage, len(s.Metadata))
		for k, v := range s.Metadata {
			meta[k] = append(json.RawMessage(nil), v...)
		}
	}
	var runs []Run
	if len(s.Runs) > 0 {
		runs = make([]Run, len(s.Runs))
		copy(runs, s.Runs)
	}
	return &Session{
		ID: s.ID, Summary: s.Summary, Messages: msgs,
		PendingRunID: s.PendingRunID, UpdatedAt: s.UpdatedAt,
		Usage: s.Usage, Runs: runs, Metadata: meta,
	}
}

// Manager 用 Store(+ 可选 Summarizer)给 Agent 加会话记忆。
type Manager struct {
	Store      Store
	Summarizer *Summarizer
}

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

func (m *Manager) persist(ctx context.Context, sess *Session, newMsgs []llm.Message, intermediates []llm.Message, assistant, runID string) error {
	sess.Messages = append(sess.Messages, newMsgs...)
	sess.Messages = append(sess.Messages, intermediates...)
	sess.Messages = append(sess.Messages, llm.Message{Role: llm.Assistant, Content: assistant})
	if m.Summarizer != nil {
		if err := m.Summarizer.compress(ctx, sess); err != nil {
			return err
		}
	}
	sess.UpdatedAt = time.Now()
	if err := m.Store.Save(ctx, sess); err != nil {
		return err
	}
	if es, ok := m.Store.(EventStore); ok {
		msgs := append(append([]llm.Message{}, newMsgs...), intermediates...)
		if assistant != "" {
			msgs = append(msgs, llm.Message{Role: llm.Assistant, Content: assistant})
		}
		if err := RecordMessages(ctx, es, sess.ID, 0, runID, msgs); err != nil {
			return err
		}
	}
	return nil
}

// Run 以会话 id 跑一轮:req.Messages 只放**本轮新输入**。
func (m *Manager) Run(ctx context.Context, id string, a agent.Agent, req llm.Request) agent.RunOutcome {
	return agent.CollectOutcome(m.RunIter(ctx, id, a, req))
}

// RunIter 与 Run 相同的会话编排,但返回事件流。
func (m *Manager) RunIter(ctx context.Context, id string, a agent.Agent, req llm.Request) iter.Seq2[agent.Event, error] {
	return func(yield func(agent.Event, error) bool) {
		sess, newMsgs, full, err := m.prepare(ctx, id, req)
		if err != nil {
			yield(agent.Event{Type: agent.EventError, Err: err}, err)
			return
		}
		var intermediates []llm.Message
		var final *llm.Response
		var runID string
		var log turnLog

		for ev, err := range a.Run(ctx, full) {
			if err != nil {
				yield(ev, err)
				return
			}
			log.see(ev)
			runID = ev.RunID
			switch ev.Type {
			case agent.EventFinal:
				final = ev.Response
				continue
			case agent.EventPaused:
				log.commit(sess)
				sess.Messages = append(sess.Messages, newMsgs...)
				sess.Messages = append(sess.Messages, intermediates...)
				sess.PendingRunID = ev.RunID
				sess.UpdatedAt = time.Now()
				if err := m.Store.Save(ctx, sess); err != nil {
					yield(agent.Event{Type: agent.EventError, Err: err}, err)
					return
				}
				if es, ok := m.Store.(EventStore); ok {
					msgs := append(append([]llm.Message{}, newMsgs...), intermediates...)
					_ = RecordMessages(ctx, es, id, 0, ev.RunID, msgs)
				}
				yield(ev, nil)
				return
			case agent.EventStep:
				if ev.Response != nil && len(ev.Response.ToolCalls) > 0 {
					intermediates = append(intermediates, llm.Message{Role: llm.Assistant, Content: ev.Response.Content, ToolCalls: ev.Response.ToolCalls})
				}
			case agent.EventToolResult:
				if ev.ToolCall != nil {
					intermediates = append(intermediates, llm.Message{Role: llm.Tool, ToolCallID: ev.ToolCall.ID, Content: ev.Result})
				}
			case agent.EventError:
				log.commit(sess)
				sess.UpdatedAt = time.Now()
				_ = m.Store.Save(ctx, sess)
				yield(ev, ev.Err)
				return
			}
			if !yield(ev, nil) {
				return
			}
		}

		if final == nil {
			err := errString("session: agent run ended without final event")
			yield(agent.Event{Type: agent.EventError, Err: err}, err)
			return
		}
		sess.PendingRunID = ""
		log.commit(sess)
		if err := m.persist(ctx, sess, newMsgs, intermediates, final.Content, runID); err != nil {
			yield(agent.Event{Type: agent.EventError, Response: final, Err: err}, err)
			return
		}
		yield(agent.Event{Type: agent.EventFinal, Response: final, RunID: runID}, nil)
	}
}

// Continue 恢复会话上暂停的 run,完成后写入会话。
func (m *Manager) Continue(ctx context.Context, id string, a agent.Agent, resolutions []agent.Resolution) agent.RunOutcome {
	return agent.CollectOutcome(m.ContinueIter(ctx, id, a, resolutions))
}

// ContinueIter 与 Continue 相同,但返回事件流。
func (m *Manager) ContinueIter(ctx context.Context, id string, a agent.Agent, resolutions []agent.Resolution) iter.Seq2[agent.Event, error] {
	return func(yield func(agent.Event, error) bool) {
		sess, err := m.Store.Load(ctx, id)
		if err != nil {
			yield(agent.Event{Type: agent.EventError, Err: err}, err)
			return
		}
		if sess == nil || sess.PendingRunID == "" {
			yield(agent.Event{Type: agent.EventError, Err: errNoPending}, errNoPending)
			return
		}

		var intermediates []llm.Message
		var final *llm.Response
		var runID string
		var log turnLog
		for ev, err := range a.Continue(ctx, sess.PendingRunID, resolutions) {
			if err != nil {
				yield(ev, err)
				return
			}
			log.see(ev)
			runID = ev.RunID
			switch ev.Type {
			case agent.EventFinal:
				final = ev.Response
				continue
			case agent.EventPaused:
				log.commit(sess)
				sess.Messages = append(sess.Messages, intermediates...)
				sess.PendingRunID = ev.RunID
				sess.UpdatedAt = time.Now()
				if err := m.Store.Save(ctx, sess); err != nil {
					yield(agent.Event{Type: agent.EventError, Err: err}, err)
					return
				}
				if es, ok := m.Store.(EventStore); ok {
					_ = RecordMessages(ctx, es, id, 0, ev.RunID, intermediates)
				}
				yield(ev, nil)
				return
			case agent.EventStep:
				if ev.Response != nil && len(ev.Response.ToolCalls) > 0 {
					intermediates = append(intermediates, llm.Message{Role: llm.Assistant, Content: ev.Response.Content, ToolCalls: ev.Response.ToolCalls})
				}
			case agent.EventToolResult:
				if ev.ToolCall != nil {
					intermediates = append(intermediates, llm.Message{Role: llm.Tool, ToolCallID: ev.ToolCall.ID, Content: ev.Result})
				}
			case agent.EventError:
				log.commit(sess)
				sess.UpdatedAt = time.Now()
				_ = m.Store.Save(ctx, sess)
				yield(ev, ev.Err)
				return
			}
			if !yield(ev, nil) {
				return
			}
		}

		if final == nil {
			err := errString("session: agent run ended without final event")
			yield(agent.Event{Type: agent.EventError, Err: err}, err)
			return
		}
		sess.PendingRunID = ""
		content := final.Content
		log.commit(sess)
		if err := m.persist(ctx, sess, nil, intermediates, content, runID); err != nil {
			yield(agent.Event{Type: agent.EventError, Err: err}, err)
			return
		}
		yield(agent.Event{Type: agent.EventFinal, Response: final, RunID: runID}, nil)
	}
}

var errNoPending = errString("session: no pending paused run")

type errString string

func (e errString) Error() string { return string(e) }

func joinSystem(base, extra string) string {
	if base == "" {
		return extra
	}
	return base + "\n\n" + extra
}

// Summarizer 在会话消息数超过 MaxMessages 时,把最早的一批折叠进 Summary,只保留最近 KeepRecent 条。
// MaxInputTokens > 0 时,摘要请求只带放得下的完整前缀,超出的旧消息留在会话里等下一轮。
type Summarizer struct {
	Client      llm.Client
	Model       string
	MaxMessages int
	KeepRecent  int
	// MaxInputTokens 是摘要请求的输入预算(估算)。0 表示不按 token 截断。
	MaxInputTokens int
	// Estimate 估算一段文字的 token。nil 时与 compaction.DefaultEstimate 相同。
	Estimate func(string) int
}

func (s *Summarizer) estimate(text string) int {
	if s.Estimate != nil {
		return s.Estimate(text)
	}
	return compaction.DefaultEstimate(text)
}

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
	if cut <= 0 {
		return nil
	}
	take, body, ok := s.prefix(sess.Summary, sess.Messages[:cut])
	if !ok {
		return nil
	}

	resp, err := s.Client.Generate(ctx, llm.Request{
		Model:    s.Model,
		System:   "你是对话摘要器。把下面的对话压缩成简洁、信息完整的中文摘要,保留关键事实、决定与未决事项。只输出摘要本身。",
		Messages: []llm.Message{{Role: llm.User, Content: body}},
	})
	if err != nil {
		return err
	}
	sess.Summary = strings.TrimSpace(resp.Content)
	sess.Messages = append([]llm.Message{}, sess.Messages[take:]...)
	return nil
}

// prefix 选出从最旧消息开始、能放进预算的完整前缀。
// 一条消息的正文是 Content,再加上文本内容块;图片块不进摘要。
// 预算放不下一整条时停在上一条,不把一条消息拆开。
func (s *Summarizer) prefix(summary string, older []llm.Message) (take int, body string, ok bool) {
	var b strings.Builder
	if summary != "" {
		b.WriteString("已有摘要:\n")
		b.WriteString(summary)
		b.WriteString("\n\n新增对话:\n")
	}
	used := 0
	if b.Len() > 0 {
		used = s.estimate(b.String())
	}
	budget := s.MaxInputTokens
	included := false
	for i, msg := range older {
		line := formatSummaryLine(msg)
		if line != "" {
			cost := s.estimate(line)
			if budget > 0 && used+cost > budget {
				break
			}
			used += cost
			b.WriteString(line)
			included = true
		}
		take = i + 1
	}
	if budget <= 0 {
		return len(older), b.String(), true
	}
	if !included {
		return 0, "", false
	}
	return take, b.String(), true
}

func formatSummaryLine(m llm.Message) string {
	text := messageText(m)
	if text == "" {
		return ""
	}
	return string(m.Role) + ": " + text + "\n"
}

// messageText 把一条消息里要进摘要的文字拼起来:正文在前,文本内容块按原顺序接在后面。
func messageText(m llm.Message) string {
	var b strings.Builder
	if m.Content != "" {
		b.WriteString(m.Content)
	}
	for _, p := range m.Parts {
		if p.Type != llm.PartText || p.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(p.Text)
	}
	return b.String()
}
