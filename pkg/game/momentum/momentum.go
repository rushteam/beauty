// Package momentum 提供"连击 + 热度时间衰减"原语:按 key 追踪连续触发的连击数,
// 并维护一个随时间指数衰减的热度值。纯内存、并发安全。
//
// 两个维度:
//   - 连击(combo):在 comboWindow 内连续 Hit,连击数递增;一旦间隔超过窗口,
//     连击断开、下次 Hit 从 1 重新开始。用于"3 连击 ×2""礼物连送 combo"特效。
//   - 热度(value):每次 Hit 累加权重,但热度随时间按半衰期指数衰减——最近的
//     触发贡献大,久远的自然冷却。用于直播间"人气值/热度榜",无需定时清零。
//
// 与相邻原语的区别:
//   - counter 是窗口内"精确累计次数"(用于配额),不衰减;momentum 是"带衰减的
//     热度 + 连击态",反映"当下有多热",适合排序与特效触发;
//   - leaderboard 排的是静态分数,momentum 的 Value 是动态冷却的实时热度。
//
// 衰减为惰性计算(读时按经过时间折算),无后台 goroutine。空闲 key 由可选的
// GC 回收。并发安全(分片锁)。零值不可用,用 New 构造。
package momentum

import (
	"hash/maphash"
	"math"
	"sync"
	"time"
)

const shardCount = 16

// config 配置。
type config struct {
	comboWindow time.Duration
	halfLife    time.Duration
}

// Option 配置 Tracker。
type Option func(*config)

// WithComboWindow 设置连击窗口:两次 Hit 间隔超过它则连击断开(默认 2s)。
func WithComboWindow(d time.Duration) Option {
	return func(c *config) { c.comboWindow = d }
}

// WithHalfLife 设置热度半衰期:每过一个半衰期,热度值衰减一半(默认 30s)。
// 越短冷却越快(适合瞬时热度),越长记忆越久(适合累积人气)。
func WithHalfLife(d time.Duration) Option {
	return func(c *config) { c.halfLife = d }
}

// State 某个 key 的当前动量快照。
type State struct {
	Combo    int     // 当前连击数(断连后为 0,首次 Hit 为 1)
	Value    float64 // 当前热度(已按时间衰减)
	MaxCombo int     // 历史最高连击
}

// keyState 单 key 的内部状态。
type keyState struct {
	combo    int
	maxCombo int
	value    float64
	lastHit  int64 // 上次 Hit 时刻(unix nano),用于连击断连判断
	lastCalc int64 // 上次衰减计算时刻(unix nano)
}

type shard struct {
	mu   sync.Mutex
	keys map[string]*keyState
}

// Tracker 连击/热度追踪器。零值不可用,用 New 构造。并发安全。
type Tracker struct {
	comboWindow int64   // ns
	decayLambda float64 // ln2 / halfLifeNanos,指数衰减系数
	seed        maphash.Seed
	shards      [shardCount]*shard
}

// New 创建追踪器。
func New(opts ...Option) *Tracker {
	cfg := config{comboWindow: 2 * time.Second, halfLife: 30 * time.Second}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.comboWindow <= 0 {
		cfg.comboWindow = 2 * time.Second
	}
	if cfg.halfLife <= 0 {
		cfg.halfLife = 30 * time.Second
	}
	t := &Tracker{
		comboWindow: int64(cfg.comboWindow),
		decayLambda: math.Ln2 / float64(cfg.halfLife),
		seed:        maphash.MakeSeed(),
	}
	for i := range t.shards {
		t.shards[i] = &shard{keys: make(map[string]*keyState)}
	}
	return t
}

func (t *Tracker) shardFor(key string) *shard {
	return t.shards[maphash.String(t.seed, key)&(shardCount-1)]
}

// decayLocked 把 ks.value 衰减到 now 时刻。调用方持 shard 锁。
func (t *Tracker) decayLocked(ks *keyState, now int64) {
	if ks.value == 0 {
		ks.lastCalc = now
		return
	}
	elapsed := float64(now - ks.lastCalc)
	if elapsed > 0 {
		ks.value *= math.Exp(-t.decayLambda * elapsed)
		ks.lastCalc = now
	}
}

// Hit 触发一次,权重 weight(通常 1;礼物可按价值传更大值)。
// 更新连击(断连则重置为 1)与热度(累加 weight),返回更新后的 State。
func (t *Tracker) Hit(key string, weight float64) State {
	now := time.Now().UnixNano()
	s := t.shardFor(key)
	s.mu.Lock()
	defer s.mu.Unlock()

	ks := s.keys[key]
	if ks == nil {
		ks = &keyState{lastCalc: now}
		s.keys[key] = ks
	}
	// 连击:窗口内递增,超窗重置。
	if ks.combo > 0 && now-ks.lastHit > t.comboWindow {
		ks.combo = 0
	}
	ks.combo++
	if ks.combo > ks.maxCombo {
		ks.maxCombo = ks.combo
	}
	ks.lastHit = now
	// 热度:先衰减到现在,再累加。
	t.decayLocked(ks, now)
	ks.value += weight

	return State{Combo: ks.combo, Value: ks.value, MaxCombo: ks.maxCombo}
}

// State 返回 key 的当前状态(读时结算连击断连与热度衰减,不产生新触发)。
func (t *Tracker) State(key string) State {
	now := time.Now().UnixNano()
	s := t.shardFor(key)
	s.mu.Lock()
	defer s.mu.Unlock()

	ks := s.keys[key]
	if ks == nil {
		return State{}
	}
	combo := ks.combo
	if combo > 0 && now-ks.lastHit > t.comboWindow {
		combo = 0 // 读时发现已断连(不改存储,Hit 时才真正重置)
	}
	t.decayLocked(ks, now)
	return State{Combo: combo, Value: ks.value, MaxCombo: ks.maxCombo}
}

// Value 返回 key 的当前热度(已衰减)。等价 State(key).Value。
func (t *Tracker) Value(key string) float64 {
	return t.State(key).Value
}

// Combo 返回 key 的当前连击数(已判断断连)。等价 State(key).Combo。
func (t *Tracker) Combo(key string) int {
	return t.State(key).Combo
}

// Reset 清除 key 的所有状态。
func (t *Tracker) Reset(key string) {
	s := t.shardFor(key)
	s.mu.Lock()
	delete(s.keys, key)
	s.mu.Unlock()
}

// GC 回收热度已衰减到近乎为零、且连击已断的空闲 key,释放内存。
// 无后台 goroutine——由调用方按需周期调用(如每分钟一次)。
// threshold 为热度回收阈值(低于它视为已冷却,建议 1e-3 量级)。返回回收的 key 数。
func (t *Tracker) GC(threshold float64) int {
	now := time.Now().UnixNano()
	var removed int
	for _, s := range t.shards {
		s.mu.Lock()
		for k, ks := range s.keys {
			t.decayLocked(ks, now)
			comboActive := ks.combo > 0 && now-ks.lastHit <= t.comboWindow
			if !comboActive && ks.value < threshold {
				delete(s.keys, k)
				removed++
			}
		}
		s.mu.Unlock()
	}
	return removed
}

// Len 返回当前追踪的 key 数(用于观测)。
func (t *Tracker) Len() int {
	var n int
	for _, s := range t.shards {
		s.mu.Lock()
		n += len(s.keys)
		s.mu.Unlock()
	}
	return n
}
