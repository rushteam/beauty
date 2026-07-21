// Package session 给 llm/agent 加"会话记忆":把多轮对话历史持久化,并在超长时滚动摘要。
// Manager 是 Runner 之上的薄编排——加载历史 → 拼进请求(旧摘要作为系统上下文 + 历史消息) →
// 跑 Runner → 回写本轮 user/assistant → 按需摘要 → 保存。
//
// 边界(机制而非策略):存哪(内存/sqldb/redis)由 Store 决定;何时摘要、保留多少条、摘要用哪个
// 模型都可配。本包只做接口 + 内存实现 + 编排,纯标准库,零外部依赖。
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
	ID        string
	Summary   string // 早期消息被折叠成的摘要(可为空)
	Messages  []llm.Message
	UpdatedAt time.Time
}

// Store 持久化会话。Load 对不存在的 id 应返回 (nil, nil) 而非错误。
type Store interface {
	Load(ctx context.Context, id string) (*Session, error)
	Save(ctx context.Context, s *Session) error
}

// MemoryStore 是并发安全的内存 Store(测试/单机用;生产可在 contrib 实现 sqldb/redis 版)。
type MemoryStore struct {
	mu sync.RWMutex
	m  map[string]*Session
}

// NewMemoryStore 创建内存 Store。
func NewMemoryStore() *MemoryStore { return &MemoryStore{m: map[string]*Session{}} }

// Load 返回会话的深拷贝(避免调用方与存储共享底层切片)。
func (s *MemoryStore) Load(_ context.Context, id string) (*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.m[id]
	if !ok {
		return nil, nil
	}
	return cloneSession(sess), nil
}

// Save 存入会话的深拷贝。
func (s *MemoryStore) Save(_ context.Context, sess *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[sess.ID] = cloneSession(sess)
	return nil
}

func cloneSession(s *Session) *Session {
	msgs := make([]llm.Message, len(s.Messages))
	copy(msgs, s.Messages)
	return &Session{ID: s.ID, Summary: s.Summary, Messages: msgs, UpdatedAt: s.UpdatedAt}
}

// Manager 用 Store(+ 可选 Summarizer)给 Runner 加会话记忆。
type Manager struct {
	Store      Store
	Summarizer *Summarizer // 为 nil 则不摘要(历史会一直增长)
}

// Run 以会话 id 跑一轮:req.Messages 只放**本轮新输入**(通常一条 user 消息);Manager 负责
// 把历史与摘要拼进去,跑完把本轮 user 输入与最终 assistant 回复追加进会话并保存。
func (m *Manager) Run(ctx context.Context, id string, r *agent.Runner, req llm.Request) (*llm.Response, error) {
	sess, err := m.Store.Load(ctx, id)
	if err != nil {
		return nil, err
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

	resp, err := r.Run(ctx, full)
	if err != nil {
		return resp, err
	}

	sess.Messages = append(sess.Messages, newMsgs...)
	sess.Messages = append(sess.Messages, llm.Message{Role: llm.Assistant, Content: resp.Content})
	if m.Summarizer != nil {
		if err := m.Summarizer.compress(ctx, sess); err != nil {
			return resp, err
		}
	}
	sess.UpdatedAt = time.Now()
	if err := m.Store.Save(ctx, sess); err != nil {
		return resp, err
	}
	return resp, nil
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
