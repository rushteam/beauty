// Package mail 提供游戏内信箱原语:泛型附件 + 领取状态 + 过期 + 批量发送 + Store 接口。
//
// 核心概念:
//   - Mail[T]: 一封邮件,T 为附件类型(由业务定义)
//   - Mailbox[T]: 管理器,提供投递/拉取/已读/领取/删除/红点等操作
//   - Store[T]: 持久化接口,内存实现用于开发测试,生产接 DB
//
// 与 notification 区别: notification 是瞬时推送(轻量); mail 带附件且有领取状态机(未领→已领)。
// 与 inbox 区别: inbox 是点对点离线消息(纯文本); mail 带泛型附件且有过期机制。
//
// 纯标准库、并发安全。
package mail

import (
	"errors"
	"sync"
	"time"
)

var (
	// ErrNotFound 邮件不存在。
	ErrNotFound = errors.New("mail: not found")
	// ErrAlreadyClaimed 附件已领取。
	ErrAlreadyClaimed = errors.New("mail: already claimed")
	// ErrExpired 邮件已过期。
	ErrExpired = errors.New("mail: expired")
	// ErrNoAttachment 邮件没有附件。
	ErrNoAttachment = errors.New("mail: no attachment")
	// ErrMailboxFull 信箱已满。
	ErrMailboxFull = errors.New("mail: mailbox full")
)

// Status 邮件状态。
type Status int

const (
	StatusUnread  Status = iota // 未读
	StatusRead                  // 已读
	StatusClaimed               // 已领取附件
)

func (s Status) String() string {
	switch s {
	case StatusUnread:
		return "unread"
	case StatusRead:
		return "read"
	case StatusClaimed:
		return "claimed"
	default:
		return "unknown"
	}
}

// Mail 表示一封邮件。T 为附件类型。
type Mail[T any] struct {
	ID          string
	RecipientID string
	SenderID    string
	Title       string
	Body        string
	Attachments []T
	Status      Status
	CreatedAt   time.Time
	ExpiresAt   time.Time // 零值表示永不过期
}

// IsExpired 判断邮件是否已过期。
func (m *Mail[T]) IsExpired(now time.Time) bool {
	return !m.ExpiresAt.IsZero() && now.After(m.ExpiresAt)
}

// HasAttachment 判断是否有附件。
func (m *Mail[T]) HasAttachment() bool {
	return len(m.Attachments) > 0
}

// Filter 查询过滤条件。
type Filter struct {
	StatusIn []Status // 为空则不过滤
	Limit    int      // 0 表示不限
	Offset   int
}

// Store 持久化接口。
type Store[T any] interface {
	Save(mail *Mail[T]) error
	Get(id string) (*Mail[T], error)
	Update(mail *Mail[T]) error
	Delete(id string) error
	List(recipientID string, filter Filter) ([]*Mail[T], error)
	CountByStatus(recipientID string, status Status) (int, error)
	DeleteExpired(now time.Time) (int, error)
}

// Option 配置 Mailbox。
type Option func(*config)

type config struct {
	maxPerUser int
	defaultTTL time.Duration
	onClaim    func(mailID string, recipientID string)
}

// WithMaxPerUser 设置每用户信箱容量上限(默认 100)。
func WithMaxPerUser(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.maxPerUser = n
		}
	}
}

// WithDefaultTTL 设置默认邮件过期时间(默认 30 天)。
func WithDefaultTTL(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.defaultTTL = d
		}
	}
}

// WithOnClaim 注册领取回调(用于发奖埋点)。
func WithOnClaim(fn func(mailID string, recipientID string)) Option {
	return func(c *config) { c.onClaim = fn }
}

// Mailbox 信箱管理器。并发安全(依赖 Store 实现的并发安全性)。
type Mailbox[T any] struct {
	cfg   config
	store Store[T]
}

// NewMailbox 创建信箱管理器。
func NewMailbox[T any](store Store[T], opts ...Option) *Mailbox[T] {
	cfg := config{
		maxPerUser: 100,
		defaultTTL: 30 * 24 * time.Hour,
	}
	for _, o := range opts {
		o(&cfg)
	}
	return &Mailbox[T]{cfg: cfg, store: store}
}

// Send 投递一封邮件。自动填充 CreatedAt 和 ExpiresAt(如果为零值)。
func (mb *Mailbox[T]) Send(m *Mail[T]) error {
	now := time.Now()
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	if m.ExpiresAt.IsZero() && mb.cfg.defaultTTL > 0 {
		m.ExpiresAt = m.CreatedAt.Add(mb.cfg.defaultTTL)
	}
	m.Status = StatusUnread

	count, err := mb.store.CountByStatus(m.RecipientID, StatusUnread)
	if err != nil {
		return err
	}
	total, err := mb.store.CountByStatus(m.RecipientID, StatusRead)
	if err != nil {
		return err
	}
	claimed, err := mb.store.CountByStatus(m.RecipientID, StatusClaimed)
	if err != nil {
		return err
	}
	if count+total+claimed >= mb.cfg.maxPerUser {
		return ErrMailboxFull
	}

	return mb.store.Save(m)
}

// BatchSend 群发邮件(模板实例化): 向多个收件人发送相同内容的邮件。
// idGen 为邮件 ID 生成器(每封邮件需唯一 ID)。
func (mb *Mailbox[T]) BatchSend(recipientIDs []string, template *Mail[T], idGen func() string) []error {
	var errs []error
	for _, rid := range recipientIDs {
		m := &Mail[T]{
			ID:          idGen(),
			RecipientID: rid,
			SenderID:    template.SenderID,
			Title:       template.Title,
			Body:        template.Body,
			Attachments: template.Attachments,
		}
		if err := mb.Send(m); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// List 拉取邮件列表(支持过滤)。自动过滤已过期邮件。
func (mb *Mailbox[T]) List(recipientID string, filter Filter) ([]*Mail[T], error) {
	mails, err := mb.store.List(recipientID, filter)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	result := mails[:0]
	for _, m := range mails {
		if !m.IsExpired(now) {
			result = append(result, m)
		}
	}
	return result, nil
}

// Read 标记邮件为已读。
func (mb *Mailbox[T]) Read(id string) error {
	m, err := mb.store.Get(id)
	if err != nil {
		return err
	}
	if m.IsExpired(time.Now()) {
		return ErrExpired
	}
	if m.Status == StatusUnread {
		m.Status = StatusRead
		return mb.store.Update(m)
	}
	return nil
}

// Claim 领取附件(仅一次)。返回附件内容。
func (mb *Mailbox[T]) Claim(id string) ([]T, error) {
	m, err := mb.store.Get(id)
	if err != nil {
		return nil, err
	}
	if m.IsExpired(time.Now()) {
		return nil, ErrExpired
	}
	if m.Status == StatusClaimed {
		return nil, ErrAlreadyClaimed
	}
	if !m.HasAttachment() {
		return nil, ErrNoAttachment
	}
	m.Status = StatusClaimed
	if err := mb.store.Update(m); err != nil {
		return nil, err
	}
	if mb.cfg.onClaim != nil {
		mb.cfg.onClaim(m.ID, m.RecipientID)
	}
	return m.Attachments, nil
}

// Delete 删除邮件。
func (mb *Mailbox[T]) Delete(id string) error {
	return mb.store.Delete(id)
}

// DeleteExpired 清理所有过期邮件,返回删除数量。
func (mb *Mailbox[T]) DeleteExpired() (int, error) {
	return mb.store.DeleteExpired(time.Now())
}

// Unread 返回未读邮件数(红点用)。
func (mb *Mailbox[T]) Unread(recipientID string) (int, error) {
	return mb.store.CountByStatus(recipientID, StatusUnread)
}

// MemoryStore 内存实现(开发/测试用)。并发安全。
type MemoryStore[T any] struct {
	mu    sync.RWMutex
	mails map[string]*Mail[T]
	order []string // 维护插入顺序
}

// NewMemoryStore 创建内存 Store。
func NewMemoryStore[T any]() *MemoryStore[T] {
	return &MemoryStore[T]{
		mails: make(map[string]*Mail[T]),
	}
}

func (s *MemoryStore[T]) Save(m *Mail[T]) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.mails[m.ID]; !exists {
		s.order = append(s.order, m.ID)
	}
	s.mails[m.ID] = m
	return nil
}

func (s *MemoryStore[T]) Get(id string) (*Mail[T], error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.mails[id]
	if !ok {
		return nil, ErrNotFound
	}
	return m, nil
}

func (s *MemoryStore[T]) Update(m *Mail[T]) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.mails[m.ID]; !ok {
		return ErrNotFound
	}
	s.mails[m.ID] = m
	return nil
}

func (s *MemoryStore[T]) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.mails[id]; !ok {
		return ErrNotFound
	}
	delete(s.mails, id)
	for i, oid := range s.order {
		if oid == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	return nil
}

func (s *MemoryStore[T]) List(recipientID string, filter Filter) ([]*Mail[T], error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	statusSet := make(map[Status]bool)
	for _, st := range filter.StatusIn {
		statusSet[st] = true
	}

	var result []*Mail[T]
	for _, id := range s.order {
		m := s.mails[id]
		if m.RecipientID != recipientID {
			continue
		}
		if len(statusSet) > 0 && !statusSet[m.Status] {
			continue
		}
		result = append(result, m)
	}

	// 未读优先排序
	sortByStatus(result)

	if filter.Offset > 0 {
		if filter.Offset >= len(result) {
			return nil, nil
		}
		result = result[filter.Offset:]
	}
	if filter.Limit > 0 && len(result) > filter.Limit {
		result = result[:filter.Limit]
	}
	return result, nil
}

func (s *MemoryStore[T]) CountByStatus(recipientID string, status Status) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, m := range s.mails {
		if m.RecipientID == recipientID && m.Status == status {
			n++
		}
	}
	return n, nil
}

func (s *MemoryStore[T]) DeleteExpired(now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, m := range s.mails {
		if m.IsExpired(now) {
			delete(s.mails, id)
			n++
		}
	}
	if n > 0 {
		cleaned := s.order[:0]
		for _, id := range s.order {
			if _, ok := s.mails[id]; ok {
				cleaned = append(cleaned, id)
			}
		}
		s.order = cleaned
	}
	return n, nil
}

func sortByStatus[T any](mails []*Mail[T]) {
	// 简单稳定排序:未读在前
	n := len(mails)
	for i := 1; i < n; i++ {
		for j := i; j > 0 && mails[j].Status < mails[j-1].Status; j-- {
			mails[j], mails[j-1] = mails[j-1], mails[j]
		}
	}
}
