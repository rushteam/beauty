// Package idgen 提供分布式唯一 ID 生成器(Snowflake 算法),纯标准库实现。
//
// 与 pkg/utils/uuid 的区别:uuid 生成 128 位字符串(UUIDv7),适合做外部可见的
// 资源标识;本包生成 64 位整数 ID,趋势递增、可排序、占用小,适合做数据库主键 /
// 对局 ID / 订单号 / 消息序号——凡是"要全局唯一 + 趋势递增 + 存储紧凑"的场景。
//
// 64 位布局(经典 Snowflake):
//
//	 1 bit  符号位,恒为 0(保证生成的 int64 为正)
//	41 bit  毫秒时间戳(相对 epoch,约可用 69 年)
//	10 bit  节点 ID(0..1023,同一部署内每个实例分配唯一值,避免跨节点冲突)
//	12 bit  同一毫秒内的序列号(0..4095,单节点单毫秒最多 4096 个 ID)
//
// 即单节点理论峰值 409.6 万 ID/秒。跨节点靠不同 node ID 保证不冲突,
// 同节点靠序列号 + 毫秒时间戳保证不冲突。
//
// 时钟回拨:NTP 校时或手动改时间可能让时钟倒退,导致重复 ID。本包检测到回拨时:
//   - 回拨在 maxBackwardWait(默认 5ms)内:自旋等待时钟追上,再生成;
//   - 回拨超过该阈值:返回错误(Next),或 panic(MustNext)——由调用方决策,
//     不静默生成可能重复的 ID。
//
// 并发安全(单锁保护序列号推进)。零值不可用,用 New 构造。
package idgen

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	nodeBits     = 10
	sequenceBits = 12

	maxNode     = -1 ^ (-1 << nodeBits)     // 1023
	maxSequence = -1 ^ (-1 << sequenceBits) // 4095

	timeShift = nodeBits + sequenceBits // 22:时间戳左移量
	nodeShift = sequenceBits            // 12:节点 ID 左移量
)

// defaultEpoch 默认起始纪元:2024-01-01 00:00:00 UTC 的毫秒时间戳。
// 41 位毫秒时间戳从此刻起算,约可用到 2093 年。
const defaultEpoch int64 = 1704067200000

// ErrClockBackwards 时钟回拨超过可容忍阈值,拒绝生成 ID(避免重复)。
var ErrClockBackwards = errors.New("idgen: clock moved backwards beyond tolerance")

// config 配置。
type config struct {
	epoch           int64
	maxBackwardWait time.Duration
}

// Option 配置 Generator。
type Option func(*config)

// WithEpoch 设置起始纪元(毫秒时间戳)。默认 2024-01-01 UTC。
// 一经上线不可更改——改了会与历史 ID 的时间段重叠,可能产生重复。
func WithEpoch(epochMillis int64) Option {
	return func(c *config) { c.epoch = epochMillis }
}

// WithMaxBackwardWait 设置可容忍的时钟回拨自旋等待上限(默认 5ms)。
// 回拨在此范围内自旋等待时钟追上;超过则 Next 报错 / MustNext panic。
func WithMaxBackwardWait(d time.Duration) Option {
	return func(c *config) { c.maxBackwardWait = d }
}

// Generator Snowflake ID 生成器。按 node ID 隔离,单实例并发安全。
// 零值不可用,用 New 构造。
type Generator struct {
	cfg  config
	node int64

	mu       sync.Mutex
	lastTime int64 // 上次生成 ID 的毫秒时间戳(相对纪元)
	sequence int64 // 当前毫秒内的序列号
}

// New 创建生成器。node 为节点 ID,范围 [0, 1023],同一部署内每实例须唯一。
// node 越界返回错误。
func New(node int64, opts ...Option) (*Generator, error) {
	if node < 0 || node > maxNode {
		return nil, fmt.Errorf("idgen: node id %d out of range [0, %d]", node, maxNode)
	}
	cfg := config{epoch: defaultEpoch, maxBackwardWait: 5 * time.Millisecond}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.maxBackwardWait < 0 {
		cfg.maxBackwardWait = 0
	}
	return &Generator{cfg: cfg, node: node}, nil
}

// now 返回相对纪元的当前毫秒时间戳。
func (g *Generator) now() int64 {
	return time.Now().UnixMilli() - g.cfg.epoch
}

// Next 生成下一个唯一 ID。
//
// 同一毫秒内序列号递增;序列号用尽(4096 个)则自旋等到下一毫秒。
// 检测到时钟回拨:在 maxBackwardWait 内自旋等待追平;超过阈值返回 ErrClockBackwards。
func (g *Generator) Next() (int64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	ts := g.now()
	if ts < g.lastTime {
		// 时钟回拨:尝试在容忍范围内自旋等待时钟追上 lastTime。
		backward := time.Duration(g.lastTime-ts) * time.Millisecond
		if backward > g.cfg.maxBackwardWait {
			return 0, fmt.Errorf("%w: %v behind", ErrClockBackwards, backward)
		}
		for ts < g.lastTime {
			ts = g.now()
		}
	}

	if ts == g.lastTime {
		// 同一毫秒:序列号递增。
		g.sequence = (g.sequence + 1) & maxSequence
		if g.sequence == 0 {
			// 序列号用尽,自旋等到下一毫秒。
			for ts <= g.lastTime {
				ts = g.now()
			}
		}
	} else {
		// 进入新毫秒:序列号归零。
		g.sequence = 0
	}
	g.lastTime = ts

	id := (ts << timeShift) | (g.node << nodeShift) | g.sequence
	return id, nil
}

// MustNext 同 Next,但出错时 panic。适合"节点 ID 已校验 + 时钟可信"的启动后稳定场景。
func (g *Generator) MustNext() int64 {
	id, err := g.Next()
	if err != nil {
		panic(err)
	}
	return id
}

// Node 返回本生成器的节点 ID。
func (g *Generator) Node() int64 { return g.node }

// Parse 从 ID 中解出各字段:相对纪元的毫秒时间戳、节点 ID、序列号。
func Parse(id int64) (tsMillis, node, sequence int64) {
	tsMillis = id >> timeShift
	node = (id >> nodeShift) & maxNode
	sequence = id & maxSequence
	return
}

// TimeOf 返回 ID 生成时刻的绝对时间(需传入生成时所用的 epoch;
// 用默认纪元生成的 ID 传 DefaultEpoch)。
func TimeOf(id, epochMillis int64) time.Time {
	tsMillis, _, _ := Parse(id)
	return time.UnixMilli(tsMillis + epochMillis)
}

// DefaultEpoch 返回默认起始纪元(毫秒时间戳),供 TimeOf 解析默认配置生成的 ID。
func DefaultEpoch() int64 { return defaultEpoch }
