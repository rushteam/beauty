// Package signin 提供每日签到原语:月 bitmap 存储 + 连签天数 + 补签 + 奖励回调。
//
// 核心思路:
//   - 用 uint32 位图表示某月签到记录(bit 0 = 1号, bit 30 = 31号)
//   - 连签天数从最后签到日往前连续计算
//   - 补签带次数限制(每月上限可配)
//   - 奖励通过回调分离(不耦合具体奖励逻辑)
//
// 与 questlog 区别: questlog 是"目标进度 + 领取"；signin 是"日历打卡 + 连续天数"。
//
// 纯标准库、并发安全。
package signin

import (
	"errors"
	"sync"
	"time"
)

var (
	// ErrAlreadySigned 今天已经签到。
	ErrAlreadySigned = errors.New("signin: already signed today")
	// ErrRetroLimit 补签次数用尽。
	ErrRetroLimit = errors.New("signin: retro sign limit reached")
	// ErrRetroFuture 不可补签未来日期。
	ErrRetroFuture = errors.New("signin: cannot retro-sign future date")
	// ErrRetroAlready 该日已签。
	ErrRetroAlready = errors.New("signin: day already signed")
	// ErrInvalidDay 无效日期。
	ErrInvalidDay = errors.New("signin: invalid day")
)

// Reward 签到产生的奖励(由调用方定义含义)。
type Reward struct {
	Type   string
	Amount int
}

// RewardFunc 奖励回调: day 为月内日(1~31), streak 为含今天的连签天数。
type RewardFunc func(day int, streak int) []Reward

// Option 配置 Record。
type Option func(*config)

type config struct {
	retroMax int        // 每月补签次数上限
	rewardFn RewardFunc // 签到奖励回调
	location *time.Location
}

// WithRetroMax 设置每月补签次数上限(默认 3)。
func WithRetroMax(n int) Option {
	return func(c *config) {
		if n >= 0 {
			c.retroMax = n
		}
	}
}

// WithRewardFunc 设置签到奖励回调。
func WithRewardFunc(fn RewardFunc) Option {
	return func(c *config) { c.rewardFn = fn }
}

// WithLocation 设置时区(默认 UTC)。用于判断"今天"是哪天。
func WithLocation(loc *time.Location) Option {
	return func(c *config) {
		if loc != nil {
			c.location = loc
		}
	}
}

// MonthKey 月份标识(YYYYMM)。
type MonthKey struct {
	Year  int
	Month time.Month
}

// Record 表示一个玩家的签到记录。并发安全。
type Record struct {
	cfg config

	mu      sync.Mutex
	months  map[MonthKey]uint32 // 每月位图
	retros  map[MonthKey]int    // 每月已用补签次数
	lastDay time.Time           // 最后签到日(零值表示从未签)
	streak  int                 // 当前连签天数
}

// New 创建新的签到记录。
func New(opts ...Option) *Record {
	cfg := config{
		retroMax: 3,
		location: time.UTC,
	}
	for _, o := range opts {
		o(&cfg)
	}
	return &Record{
		cfg:    cfg,
		months: make(map[MonthKey]uint32),
		retros: make(map[MonthKey]int),
	}
}

// SignIn 执行今日签到。幂等:同天重复返回 ErrAlreadySigned。
// 成功时返回奖励(如果配置了 RewardFunc)。
func (r *Record) SignIn(now time.Time) ([]Reward, error) {
	now = now.In(r.cfg.location)
	today := dayOnly(now)

	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.lastDay.IsZero() && dayOnly(r.lastDay) == today {
		return nil, ErrAlreadySigned
	}

	mk := monthKey(now)
	day := now.Day()
	r.months[mk] |= 1 << (day - 1)

	// 计算连签(按日历日,避免 DST 导致 24h 比较失效)
	if !r.lastDay.IsZero() && dayOnly(r.lastDay).AddDate(0, 0, 1).Equal(today) {
		r.streak++
	} else {
		r.streak = 1
	}
	r.lastDay = now

	if r.cfg.rewardFn != nil {
		return r.cfg.rewardFn(day, r.streak), nil
	}
	return nil, nil
}

// Streak 返回当前连签天数。
func (r *Record) Streak() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.streak
}

// SignedDays 返回某月已签到天数。
func (r *Record) SignedDays(year int, month time.Month) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	mk := MonthKey{Year: year, Month: month}
	return popcount(r.months[mk])
}

// IsSigned 检查某天是否已签。
func (r *Record) IsSigned(year int, month time.Month, day int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	mk := MonthKey{Year: year, Month: month}
	return r.months[mk]&(1<<(day-1)) != 0
}

// MonthBitmap 返回某月签到位图(bit 0 = 1号)。
func (r *Record) MonthBitmap(year int, month time.Month) uint32 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.months[MonthKey{Year: year, Month: month}]
}

// RetroSign 补签指定日期。受月补签次数限制。
func (r *Record) RetroSign(now time.Time, year int, month time.Month, day int) ([]Reward, error) {
	now = now.In(r.cfg.location)

	daysInMonth := daysIn(year, month)
	if day < 1 || day > daysInMonth {
		return nil, ErrInvalidDay
	}

	target := time.Date(year, month, day, 0, 0, 0, 0, r.cfg.location)
	today := dayOnly(now)
	if target.After(today) {
		return nil, ErrRetroFuture
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	mk := MonthKey{Year: year, Month: month}
	bit := uint32(1) << (day - 1)
	if r.months[mk]&bit != 0 {
		return nil, ErrRetroAlready
	}

	if r.retros[mk] >= r.cfg.retroMax {
		return nil, ErrRetroLimit
	}

	r.months[mk] |= bit
	r.retros[mk]++

	// 补签可能延长连签(如果补的是 lastDay 前一天)
	r.recalcStreak(now)

	if r.cfg.rewardFn != nil {
		return r.cfg.rewardFn(day, r.streak), nil
	}
	return nil, nil
}

// RetroRemaining 返回本月剩余补签次数。
func (r *Record) RetroRemaining(now time.Time) int {
	now = now.In(r.cfg.location)
	mk := monthKey(now)
	r.mu.Lock()
	defer r.mu.Unlock()
	used := r.retros[mk]
	rem := r.cfg.retroMax - used
	if rem < 0 {
		return 0
	}
	return rem
}

// recalcStreak 重新计算连签(补签后可能变化)。从 lastDay 向前遍历。
func (r *Record) recalcStreak(now time.Time) {
	today := dayOnly(now)
	streak := 0
	d := today
	for {
		mk := MonthKey{Year: d.Year(), Month: d.Month()}
		bit := uint32(1) << (d.Day() - 1)
		if r.months[mk]&bit == 0 {
			break
		}
		streak++
		d = d.AddDate(0, 0, -1)
	}
	r.streak = streak
	if streak > 0 {
		r.lastDay = now
	}
}

func monthKey(t time.Time) MonthKey {
	return MonthKey{Year: t.Year(), Month: t.Month()}
}

func dayOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func daysIn(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func popcount(x uint32) int {
	n := 0
	for x != 0 {
		n++
		x &= x - 1
	}
	return n
}
