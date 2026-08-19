// Package inputclock 记录客户端帧与服务器帧映射,并估算 RTT(延迟补偿入口)。
package inputclock

import (
	"sync"
	"time"
)

// Sample 是一条输入的时间戳样本。
type Sample struct {
	Player      string
	ClientFrame uint64
	ServerFrame uint64
	ReceivedAt  time.Time
}

// Clock 维护 per-player 帧映射与 RTT 估算。
type Clock struct {
	mu    sync.RWMutex
	byKey map[key]Sample
	rtt   map[string]time.Duration
	now   func() time.Time
}

type key struct {
	player      string
	clientFrame uint64
}

// Option 配置 Clock。
type Option func(*Clock)

// WithNow 注入时钟(测试用)。
func WithNow(fn func() time.Time) Option {
	return func(c *Clock) {
		if fn != nil {
			c.now = fn
		}
	}
}

// New 创建 Clock。
func New(opts ...Option) *Clock {
	c := &Clock{
		byKey: make(map[key]Sample),
		rtt:   make(map[string]time.Duration),
		now:   time.Now,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Record 记录一条输入样本;若同一 (player, clientFrame) 已存在则覆盖。
func (c *Clock) Record(s Sample) {
	if s.Player == "" || s.ReceivedAt.IsZero() {
		s.ReceivedAt = c.now()
	}
	c.mu.Lock()
	c.byKey[key{s.Player, s.ClientFrame}] = s
	c.mu.Unlock()
}

// ServerFrame 返回 clientFrame 对应的服务器帧。
func (c *Clock) ServerFrame(player string, clientFrame uint64) (uint64, bool) {
	c.mu.RLock()
	s, ok := c.byKey[key{player, clientFrame}]
	c.mu.RUnlock()
	if !ok {
		return 0, false
	}
	return s.ServerFrame, true
}

// UpdateRTT 用 ping 往返时间更新 player 的 RTT 估算(指数移动平均)。
func (c *Clock) UpdateRTT(player string, rtt time.Duration) {
	if player == "" || rtt <= 0 {
		return
	}
	c.mu.Lock()
	if prev, ok := c.rtt[player]; ok {
		c.rtt[player] = (prev*3 + rtt) / 4
	} else {
		c.rtt[player] = rtt
	}
	c.mu.Unlock()
}

// RTT 返回 player 的 RTT 估算(无样本时为 0)。
func (c *Clock) RTT(player string) time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.rtt[player]
}

// RewindFrames 按 tick 间隔估算 RTT 对应的 rewind 帧数(至少 1)。
func (c *Clock) RewindFrames(player string, tick time.Duration) uint64 {
	if tick <= 0 {
		return 1
	}
	rtt := c.RTT(player)
	if rtt <= 0 {
		return 1
	}
	n := uint64(rtt / tick)
	if n < 1 {
		return 1
	}
	return n
}

// CompensatedFrame 返回用于 lag compensation 的目标服务器帧(serverFrame - rewind)。
func (c *Clock) CompensatedFrame(player string, clientFrame uint64, tick time.Duration) (uint64, bool) {
	sf, ok := c.ServerFrame(player, clientFrame)
	if !ok {
		return 0, false
	}
	rewind := c.RewindFrames(player, tick)
	if sf <= rewind {
		return 1, true
	}
	return sf - rewind, true
}
