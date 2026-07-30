// Package energy 提供体力/精力系统原语:惰性时间回复 + 消耗 + 充值 + 倒计时计算。
//
// 不依赖定时器:调用 Current() 时根据时间差惰性补充,O(1) 无状态刷新。
// 适用于游戏体力、行动力、精力等"随时间自动恢复,消耗后等待或付费充值"的资源。
//
// 与 wallet 区别: wallet 是精确账本(不可变流水); energy 是"时间自动回复 + 消耗"的特化资源。
//
// 纯标准库、并发安全。
package energy

import (
	"errors"
	"sync"
	"time"
)

// ErrInsufficient 表示体力不足。
var ErrInsufficient = errors.New("energy: insufficient")

// Option 配置 Energy。
type Option func(*config)

type config struct {
	cap           int
	regenInterval time.Duration
	regenAmount   int
	overflow      bool // Add 时是否允许超过上限
}

// WithCap 设置体力上限(默认 100)。
func WithCap(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.cap = n
		}
	}
}

// WithRegenInterval 设置每次回复的时间间隔(默认 5 分钟)。
func WithRegenInterval(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.regenInterval = d
		}
	}
}

// WithRegenAmount 设置每个 interval 回复的数量(默认 1)。
func WithRegenAmount(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.regenAmount = n
		}
	}
}

// WithOverflow 设置 Add 充值时是否允许超过上限(默认 false,截止到上限)。
func WithOverflow(allow bool) Option {
	return func(c *config) { c.overflow = allow }
}

// Energy 表示一个体力实例。并发安全。
type Energy struct {
	cfg config

	mu         sync.Mutex
	cur        int
	lastUpdate time.Time
}

// New 创建体力实例,初始值等于上限。
func New(opts ...Option) *Energy {
	cfg := config{
		cap:           100,
		regenInterval: 5 * time.Minute,
		regenAmount:   1,
		overflow:      false,
	}
	for _, o := range opts {
		o(&cfg)
	}
	return &Energy{
		cfg:        cfg,
		cur:        cfg.cap,
		lastUpdate: time.Now(),
	}
}

// NewWithState 从持久化状态恢复(加载数据库值)。
func NewWithState(cur int, lastUpdate time.Time, opts ...Option) *Energy {
	cfg := config{
		cap:           100,
		regenInterval: 5 * time.Minute,
		regenAmount:   1,
		overflow:      false,
	}
	for _, o := range opts {
		o(&cfg)
	}
	return &Energy{
		cfg:        cfg,
		cur:        cur,
		lastUpdate: lastUpdate,
	}
}

// Current 返回当前体力值(惰性计算,自动补充自然回复)。
func (e *Energy) Current() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.regen(time.Now())
	return e.cur
}

// Cap 返回体力上限。
func (e *Energy) Cap() int { return e.cfg.cap }

// Spend 消耗 n 点体力。不足返回 ErrInsufficient。
func (e *Energy) Spend(n int) error {
	if n <= 0 {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.regen(time.Now())
	if e.cur < n {
		return ErrInsufficient
	}
	e.cur -= n
	return nil
}

// Add 充值 n 点体力。overflow=false 时截止上限; overflow=true 时可超上限。
func (e *Energy) Add(n int) int {
	if n <= 0 {
		return e.Current()
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.regen(time.Now())
	e.cur += n
	if !e.cfg.overflow && e.cur > e.cfg.cap {
		e.cur = e.cfg.cap
	}
	return e.cur
}

// TimeToFull 返回从当前值恢复到满的剩余时间。已满返回 0。
func (e *Energy) TimeToFull() time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.regen(time.Now())
	return e.timeToAmount(e.cfg.cap)
}

// TimeToAmount 返回从当前值恢复到目标值 target 的剩余时间。已达到返回 0。
func (e *Energy) TimeToAmount(target int) time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.regen(time.Now())
	return e.timeToAmount(target)
}

// Snapshot 返回当前状态(用于持久化)。
func (e *Energy) Snapshot() (cur int, lastUpdate time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.regen(time.Now())
	return e.cur, e.lastUpdate
}

func (e *Energy) timeToAmount(target int) time.Duration {
	if e.cur >= target {
		return 0
	}
	need := target - e.cur
	intervals := (need + e.cfg.regenAmount - 1) / e.cfg.regenAmount // ceil division
	return time.Duration(intervals) * e.cfg.regenInterval
}

func (e *Energy) regen(now time.Time) {
	if e.cur >= e.cfg.cap {
		e.lastUpdate = now
		return
	}
	elapsed := now.Sub(e.lastUpdate)
	if elapsed < e.cfg.regenInterval {
		return
	}
	ticks := int(elapsed / e.cfg.regenInterval)
	gained := ticks * e.cfg.regenAmount
	e.cur += gained
	if e.cur > e.cfg.cap {
		e.cur = e.cfg.cap
	}
	e.lastUpdate = e.lastUpdate.Add(time.Duration(ticks) * e.cfg.regenInterval)
}
