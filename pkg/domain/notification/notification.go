// Package notification 提供离线通知队列:用户不在线时存通知,上线后拉取,
// 与 pkg/router 的实时路由互补——router 投在线者,notification 投离线者。
//
// 设计参考 Nakama server/core_notification.go:
//   - persistent 标志区分持久通知(存 DB+在线投)与瞬时通知(仅在线投);
//   - 离线通知按 (userID, seq) 有序存储,游标分页避免重复推送;
//   - 无 read/unread 状态机——删除即消失,简化并发(参考 Nakama 只删不改)。
//
// 与 pkg/router 的分工:Send 时若用户在线(通过 liveSink)即时投递,
// 持久通知无论在线与否都存一份,供后续拉取/审计。
//
// 适用场景:IM 离线消息、系统通知、好友请求、活动奖励提醒。
//
// 零值不可用,用 New 构造。Store 并发安全。
package notification

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Notification 是一条通知。持久化后 ID/Seq 由 Store 分配。
type Notification struct {
	ID         int64  // 全局单调递增 ID
	UserID     string // 接收者
	SenderID   string // 发送者(系统为空)
	Subject    string // 主题/类型(如 "friend_request")
	Content    string // 业务自定义 JSON
	Code       int    // 分类码(业务自定义)
	Persistent bool   // 持久化:存库 + 上线可拉取;false 则仅在线投
	Seq        int64  // 用户内单调序号(游标分页用)
	CreateTime int64  // unix nano
}

// LiveSink 在线投递函数。返回 false 表示用户当前不在线。
// 实现通常用 pkg/router.SendToPresenceIDs 或 pkg/ws/session.Send。
type LiveSink func(userID string, n *Notification) bool

// Store 管理持久通知的存储、在线投递与游标拉取。
type Store struct {
	mu       sync.Mutex
	byUser   map[string][]*Notification // 每用户的持久通知(按 seq 有序)
	byUserSeq map[string]int64          // 每用户下一个 seq(单调,不因截断回退)
	seq      atomic.Int64               // 全局 ID
	live     LiveSink                   // 在线投递(可 nil)
	maxPerUser int                      // 每用户最大保留数(超出删最旧)
}

// Option 配置 Store。
type Option func(*config)

type config struct {
	maxPerUser int
}

// WithMaxPerUser 每用户最大保留通知数,超出删最旧(默认 256)。
func WithMaxPerUser(n int) Option { return func(c *config) { c.maxPerUser = n } }

// New 创建通知存储。live 为在线投递函数,nil 则不实时投(仅存)。
func New(live LiveSink, opts ...Option) *Store {
	cfg := &config{maxPerUser: 256}
	for _, o := range opts {
		o(cfg)
	}
	return &Store{
		byUser:     make(map[string][]*Notification),
		byUserSeq:  make(map[string]int64),
		live:       live,
		maxPerUser: cfg.maxPerUser,
	}
}

// Send 发送一条通知。
//   - persistent=true:存库 + 若在线则实时投(在线投失败也不影响存储);
//   - persistent=false:仅尝试在线投,不存库。投失败即丢弃(瞬时通知)。
// 返回存库后的 Notification(ID/Seq 已填充),未存库则返回 nil。
func (s *Store) Send(ctx context.Context, n *Notification) *Notification {
	n.ID = s.seq.Add(1)
	if n.CreateTime == 0 {
		n.CreateTime = time.Now().UnixNano()
	}
	if !n.Persistent {
		if s.live != nil {
			s.live(n.UserID, n)
		}
		return nil
	}
	s.mu.Lock()
	s.byUserSeq[n.UserID]++
	n.Seq = s.byUserSeq[n.UserID]
	list := append(s.byUser[n.UserID], n)
	// 超容量:删最旧。seq 仍单调(基于计数器,不因截断回退)。
	if len(list) > s.maxPerUser {
		list = list[len(list)-s.maxPerUser:]
	}
	s.byUser[n.UserID] = list
	s.mu.Unlock()
	// 在线投递:存库后尝试,投失败不影响存储(离线拉取兜底)。
	if s.live != nil {
		s.live(n.UserID, n)
	}
	return n
}

// List 拉取某用户的持久通知,支持游标分页。
// afterSeq:返回 seq > afterSeq 的通知(0 表示从头);limit<=0 默认 50。
// 返回的通知按 seq 升序。调用方下次用最后一条的 Seq 作为 afterSeq 续传。
func (s *Store) List(userID string, afterSeq int64, limit int) []Notification {
	if limit <= 0 {
		limit = 50
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.byUser[userID]
	out := make([]Notification, 0, limit)
	for _, n := range list {
		if n.Seq <= afterSeq {
			continue
		}
		out = append(out, *n)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// Delete 删除某用户指定 ID 的通知。返回是否删除成功。
func (s *Store) Delete(userID string, id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.byUser[userID]
	for i, n := range list {
		if n.ID == id {
			s.byUser[userID] = append(list[:i], list[i+1:]...)
			return true
		}
	}
	return false
}

// DeleteAll 清空某用户所有持久通知。
func (s *Store) DeleteAll(userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byUser, userID)
}

// Count 返回某用户当前持久通知数。
func (s *Store) Count(userID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byUser[userID])
}
