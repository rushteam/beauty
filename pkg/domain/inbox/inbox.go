// Package inbox 提供点对点离线消息收件箱:已读/未读、游标分页、ACK。
//
// 与 pkg/domain/notification 的分工:
//   - notification 是"系统→用户"单向通知(系统广播、运营消息),偏推送;
//   - inbox 是"用户→用户"点对点离线消息(离线私聊、离线赠礼、离线战绩),
//     带已读/未读状态与 ACK,语义更接近邮件收件箱。
//
// 两者结构相似但语义不同:notification 无已读状态(通知发出即视为送达),
// inbox 必须有已读/未读(用户要区分"新消息")。复用同一套游标分页约定。
//
// inbox/chat 持久化语义:
//   - Send 写入收件人信箱,分配全局 ID + 用户内单调 Seq;
//   - List 游标分页(afterSeq=0 取最新,翻页用上一批最小 seq);
//   - MarkRead 标记已读(单条或到某 seq 为止);
//   - UnreadCount 未读数(红点用);
//   - 在线时通过 LiveSink 即时投递,离线则留存。
//
// 零值不可用,用 New 构造。Store 并发安全。
package inbox

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Message 一条收件箱消息。持久化后 ID/Seq 由 Store 分配。
type Message struct {
	ID         int64  // 全局单调递增 ID
	OwnerID    string // 收件人(信箱归属者)
	FromID     string // 发送者(始终为用户,非系统)
	Type       string // 消息类型(如 "chat"/"gift"/"match_result")
	Content    string // 业务自定义 JSON 负载
	Seq        int64  // 收件人信箱内单调序号(游标分页用)
	Read       bool   // 是否已读
	CreateTime int64  // unix nano
}

// LiveSink 在线投递函数。返回 false 表示收件人当前不在线,消息留存信箱。
// 实现通常用 pkg/router.SendToPresenceIDs 或 pkg/ws/session.Send。
type LiveSink func(ownerID string, m *Message) bool

// Store 管理点对点离线消息的存储、在线投递、游标拉取与已读状态。
type Store struct {
	mu        sync.Mutex
	byUser    map[string][]*Message // 每用户信箱(按 seq 升序)
	byUserSeq map[string]int64      // 每用户下一个 seq(单调,不因截断回退)
	seq       atomic.Int64          // 全局 ID
	live      LiveSink              // 在线投递(可 nil)
	maxPerBox int                   // 每信箱最大保留数(超出删最旧)
}

// Option 配置 Store。
type Option func(*config)

type config struct {
	maxPerBox int
}

// WithMaxPerBox 每信箱最大保留消息数,超出删最旧(默认 500)。
func WithMaxPerBox(n int) Option { return func(c *config) { c.maxPerBox = n } }

// New 创建收件箱 Store。live 为 nil 时消息总留存(不尝试在线投递)。
func New(live LiveSink, opts ...Option) *Store {
	cfg := config{maxPerBox: 500}
	for _, o := range opts {
		o(&cfg)
	}
	return &Store{
		byUser:    make(map[string][]*Message),
		byUserSeq: make(map[string]int64),
		live:      live,
		maxPerBox: cfg.maxPerBox,
	}
}

// Send 向 toID 的信箱投递一条消息。fromID 为发送者。
// 若收件人在线(LiveSink 返回 true),仍写入信箱(便于历史拉取),但可按业务
// 选择不持久(这里选择持久——收件箱语义要求可回溯)。返回写入的消息(带 ID/Seq)。
func (s *Store) Send(ctx context.Context, toID, fromID, msgType, content string) *Message {
	now := time.Now().UnixNano()
	m := &Message{
		ID:         s.seq.Add(1),
		OwnerID:    toID,
		FromID:     fromID,
		Type:       msgType,
		Content:    content,
		CreateTime: now,
	}
	s.mu.Lock()
	seq := s.byUserSeq[toID] + 1
	s.byUserSeq[toID] = seq
	m.Seq = seq
	box := append(s.byUser[toID], m)
	// 截断:超出上限删最旧(保留最新 maxPerBox 条)。
	if len(box) > s.maxPerBox {
		box = box[len(box)-s.maxPerBox:]
	}
	s.byUser[toID] = box
	s.mu.Unlock()
	// 在线投递(不持锁,避免 LiveSink 阻塞)。
	if s.live != nil {
		s.live(toID, m)
	}
	return m
}

// List 游标分页拉取信箱。afterSeq=0 取最新 limit 条(降序);否则取 seq<afterSeq
// 的 limit 条(降序,向后翻页用上一批最小 seq)。
// 返回的消息按 seq 降序(最新在前)。
func (s *Store) List(ownerID string, afterSeq int64, limit int) []Message {
	if limit <= 0 {
		limit = 20
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	box := s.byUser[ownerID]
	if len(box) == 0 {
		return nil
	}
	// 找到 afterSeq 的边界(降序输出)。
	start := len(box)
	if afterSeq > 0 {
		// box 按 seq 升序。找第一个 seq >= afterSeq 的位置,
		// 该位置即为"seq<afterSeq 的子区间"的右端(不含),从 start-1 向下降序输出。
		start = lowerBoundSeq(box, afterSeq)
	}
	end := max(start-limit, 0)
	out := make([]Message, 0, start-end)
	for i := start - 1; i >= end; i-- {
		out = append(out, *box[i])
	}
	return out
}

// MarkRead 标记已读:单条(指定 seq)或到某 seq 为止(含)。
// 返回新标记为已读的消息数(原本未读的)。
func (s *Store) MarkRead(ownerID string, upToSeq int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	box := s.byUser[ownerID]
	n := 0
	for i := len(box) - 1; i >= 0; i-- {
		if box[i].Seq > upToSeq {
			continue
		}
		if !box[i].Read {
			box[i].Read = true
			n++
		}
	}
	return n
}

// MarkOneRead 标记单条消息已读(按 seq)。返回是否找到并成功标记。
func (s *Store) MarkOneRead(ownerID string, seq int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	box := s.byUser[ownerID]
	// 二分找 seq。
	idx := lowerBoundSeq(box, seq)
	if idx < len(box) && box[idx].Seq == seq {
		if !box[idx].Read {
			box[idx].Read = true
		}
		return true
	}
	return false
}

// UnreadCount 返回未读消息数(红点用)。
func (s *Store) UnreadCount(ownerID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	box := s.byUser[ownerID]
	n := 0
	for _, m := range box {
		if !m.Read {
			n++
		}
	}
	return n
}

// Delete 删除单条消息(按 seq)。返回是否删除成功。
func (s *Store) Delete(ownerID string, seq int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	box := s.byUser[ownerID]
	idx := lowerBoundSeq(box, seq)
	if idx >= len(box) || box[idx].Seq != seq {
		return false
	}
	s.byUser[ownerID] = append(box[:idx], box[idx+1:]...)
	return true
}

// Count 返回信箱消息总数。
func (s *Store) Count(ownerID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byUser[ownerID])
}

// lowerBoundSeq 返回 box(按 Seq 升序)中第一个 Seq >= target 的索引,
// 全部小于 target 时返回 len(box)。等价于 C++ lower_bound,基于 sort.Search。
func lowerBoundSeq(box []*Message, target int64) int {
	return sort.Search(len(box), func(i int) bool { return box[i].Seq >= target })
}
