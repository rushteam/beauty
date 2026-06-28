// Package chat 提供频道聊天消息:按 channel 持久化 + 自增 message_id 游标分页。
//
// 与 pkg/domain/notification 互补:notification 是"给个人的离线信"(按 userID 游标),
// chat 是"给频道的历史信"(按 channelID 游标)。IM 频道消息需要持久化 + 历史拉取 +
// 游标分页,区别于 pkg/match 的实时(不持久)与 pkg/router 的投递(不存历史)。
//
// channel message:
//   - 每条消息有 channel 内单调递增的 message_id(游标);
//   - Before(messageID, limit) 返回该 ID 之前的历史(翻页往前看);
//   - 频道有容量上限,超限删最旧(message_id 不因截断回退,保持单调);
//   - 实时投递与持久化解耦:本包只管存历史,实时扇出由 pkg/router 负责。
//
// 零值不可用,用 New 构造。Store 并发安全。
package chat

import (
	"sync"
	"sync/atomic"
)

// Message 一条频道消息。
type Message struct {
	ID        int64  // 全局唯一 ID(Store 分配)
	ChannelID string // 频道 ID
	MsgID     int64  // 频道内单调序号(游标分页用)
	UserID    string // 发送者
	Content   string // 消息内容(业务可自定义编码,这里用 string 简化)
	CreatedAt int64  // 创建时间(UnixNano)
}

// Store 频道消息存储:按 channel 持久化 + 游标分页。
type Store struct {
	mu        sync.Mutex
	byChannel map[string][]*Message // channelID → 消息(按 msgID 升序)
	bySeq     map[string]int64      // channelID → 下一个 msgID(单调,不因截断回退)
	idCounter atomic.Int64
	maxPerChannel int
}

// Option 配置 Store。
type Option func(*config)

type config struct {
	maxPerChannel int
}

// WithMaxPerChannel 设置每频道最大保留条数,超限删最旧。默认 1000。
func WithMaxPerChannel(n int) Option { return func(c *config) { c.maxPerChannel = n } }

// New 创建 Store。
func New(opts ...Option) *Store {
	cfg := &config{maxPerChannel: 1000}
	for _, o := range opts {
		o(cfg)
	}
	return &Store{
		byChannel:     make(map[string][]*Message),
		bySeq:         make(map[string]int64),
		maxPerChannel: cfg.maxPerChannel,
	}
}

// Post 投递一条消息到频道,返回存库后的 Message(ID/MsgID 已填充)。
// channelID/userID 为空则返回 nil 不存。
func (s *Store) Post(channelID, userID, content string, nowNano int64) *Message {
	if channelID == "" || userID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bySeq[channelID]++
	msg := &Message{
		ID:        s.idCounter.Add(1),
		ChannelID: channelID,
		MsgID:     s.bySeq[channelID],
		UserID:    userID,
		Content:   content,
		CreatedAt: nowNano,
	}
	list := append(s.byChannel[channelID], msg)
	s.byChannel[channelID] = list
	// 超容量:删最旧。msgID 仍单调(基于计数器,不因截断回退)。
	if len(list) > s.maxPerChannel {
		s.byChannel[channelID] = list[len(list)-s.maxPerChannel:]
	}
	return msg
}

// Before 返回 channelID 中 msgID < beforeID 的历史(翻页往前看)。
// beforeID<=0 表示返回最新的 limit 条。
// 返回按 msgID 降序(最新在前),便于客户端"加载更早消息"。
// limit<=0 默认 50。
func (s *Store) Before(channelID string, beforeID int64, limit int) []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.byChannel[channelID]
	if len(list) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 50
	}
	// 二分找 beforeID 的位置(msgID 升序)。
	// 收集 beforeID 之前的条目,按降序取 limit。
	var collected []Message
	for i := len(list) - 1; i >= 0; i-- {
		m := list[i]
		if beforeID > 0 && m.MsgID >= beforeID {
			continue
		}
		collected = append(collected, *m)
		if len(collected) >= limit {
			break
		}
	}
	return collected
}

// After 返回 channelID 中 msgID > afterID 的消息(拉取新消息)。
// afterID<0 表示从最新开始(返回最新 limit 条)。
// 返回按 msgID 升序(旧→新),便于客户端追加到列表尾部。
// limit<=0 默认 50。
func (s *Store) After(channelID string, afterID int64, limit int) []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.byChannel[channelID]
	if len(list) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 50
	}
	var out []Message
	for _, m := range list {
		if m.MsgID <= afterID {
			continue
		}
		out = append(out, *m)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// Latest 返回频道最新的 limit 条(按 msgID 降序)。
func (s *Store) Latest(channelID string, limit int) []Message {
	return s.Before(channelID, 0, limit)
}

// Count 返回频道当前消息数。
func (s *Store) Count(channelID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byChannel[channelID])
}

// LastMsgID 返回频道最新 msgID(无消息返回 0)。用于客户端增量拉取的游标基点。
func (s *Store) LastMsgID(channelID string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.byChannel[channelID]
	if len(list) == 0 {
		return 0
	}
	return list[len(list)-1].MsgID
}

// Delete 删除频道某条消息(by global ID)。返回是否删除成功。
func (s *Store) Delete(channelID string, id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.byChannel[channelID]
	for i, m := range list {
		if m.ID == id {
			s.byChannel[channelID] = append(list[:i], list[i+1:]...)
			if len(s.byChannel[channelID]) == 0 {
				delete(s.byChannel, channelID)
				delete(s.bySeq, channelID)
			}
			return true
		}
	}
	return false
}
