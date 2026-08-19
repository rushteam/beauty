// Package cmdlog 提供帧索引指令日志与快照检查点原语,用于断线重连追帧。
//
// 解决的问题:实时游戏中玩家网络闪断(进电梯/切网络)后重连,需要快速恢复到当前
// 游戏状态。如果从头重算——服务器瞬间宕机;如果完全靠客户端本地缓存——玩家改内存
// 作弊。本包提供"快照检查点 + 中间指令流"的折中方案。
//
// 核心机制:
//   - Log[S, C]:维护一个环形指令缓冲(按帧索引)和周期性快照检查点;
//   - Record(frame, cmds):每帧记录该帧的所有玩家指令;
//   - Checkpoint(frame, state):周期性存一次微型状态快照(如每 5 秒);
//   - Recover(since):当玩家重连时,计算断线帧号,返回"最近快照 + 之后的指令流",
//     客户端收到后从快照开始本地高速重放指令(追帧);如果断线太久(指令已被覆盖),
//     返回最新快照做全量恢复。
//
// 与相邻原语的关系:
//   - snapbuf 存服务器快照用于 lag compensation(server rewind),cmdlog 存指令流
//     用于 client replay(断线追帧)——两个方向:snapbuf 往"过去"查,cmdlog 往"未来"追;
//   - rollback 是客户端预测纠正(回滚+重演算),cmdlog 是重连时的追帧(快照+重放);
//   - replicate.Journal 存的是"状态增量 Delta",cmdlog 存的是"操作指令 Command"——
//     Journal 用于状态同步补发,cmdlog 用于确定性重放(帧同步场景);
//   - resume 恢复"你在哪些房间/流",cmdlog 恢复"房间里发生了什么"。
//
// 并发安全:Log 用 sync.Mutex 保护(tick goroutine 写 Record/Checkpoint,
// 重连 goroutine 读 Recover)。零值不可用:用 NewLog 构造。
package cmdlog

import (
	"errors"
	"sync"
)

// config 配置。
type config struct {
	cmdDepth  int
	snapDepth int
}

// Option 配置 Log。
type Option func(*config)

// WithCmdDepth 设置指令环形缓冲深度(帧数,默认 512)。
// 断线时间超过此深度对应的帧数时,无法追帧,只能全量恢复。
func WithCmdDepth(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.cmdDepth = n
		}
	}
}

// WithSnapDepth 设置快照保留数量(默认 8)。
func WithSnapDepth(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.snapDepth = n
		}
	}
}

// Log 维护帧索引的指令日志与快照检查点。S 是快照类型,C 是单帧指令类型。
type Log[S, C any] struct {
	mu sync.Mutex

	// 指令环形缓冲
	cmds     []cmdEntry[C]
	cmdHead  int
	cmdCount int
	cmdDepth int

	// 快照环形缓冲
	snaps     []snapEntry[S]
	snapHead  int
	snapCount int
	snapDepth int

	latestFrame uint64
}

type cmdEntry[C any] struct {
	frame uint64
	cmds  []C
}

type snapEntry[S any] struct {
	frame uint64
	state S
}

// NewLog 创建指令日志。
func NewLog[S, C any](opts ...Option) *Log[S, C] {
	cfg := config{cmdDepth: 512, snapDepth: 8}
	for _, o := range opts {
		o(&cfg)
	}
	return &Log[S, C]{
		cmds:      make([]cmdEntry[C], cfg.cmdDepth),
		cmdDepth:  cfg.cmdDepth,
		snaps:     make([]snapEntry[S], cfg.snapDepth),
		snapDepth: cfg.snapDepth,
	}
}

// ErrFrameNotMonotonic 帧号未单调递增。
var ErrFrameNotMonotonic = errors.New("cmdlog: frame must be monotonically increasing")

// Record 记录一帧的指令。frame 必须严格大于上一次记录的帧号(单调递增)。
func (l *Log[S, C]) Record(frame uint64, cmds []C) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cmdCount > 0 && frame <= l.latestFrame {
		return ErrFrameNotMonotonic
	}
	l.cmds[l.cmdHead] = cmdEntry[C]{frame: frame, cmds: cmds}
	l.cmdHead = (l.cmdHead + 1) % l.cmdDepth
	if l.cmdCount < l.cmdDepth {
		l.cmdCount++
	}
	l.latestFrame = frame
	return nil
}

// Checkpoint 存储一个快照检查点(由业务周期性调用,如每 N 帧一次)。
func (l *Log[S, C]) Checkpoint(frame uint64, state S) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.snaps[l.snapHead] = snapEntry[S]{frame: frame, state: state}
	l.snapHead = (l.snapHead + 1) % l.snapDepth
	if l.snapCount < l.snapDepth {
		l.snapCount++
	}
}

// Recovery 是重连恢复的结果。
type Recovery[S, C any] struct {
	// Snapshot 快照(追帧/全量恢复的起点)。
	Snapshot S
	// SnapshotFrame 快照对应的帧号。
	SnapshotFrame uint64
	// Commands 快照之后的指令流(按帧升序)。追帧场景下客户端从 Snapshot 开始
	// 依次重放这些指令即可追上。
	Commands []FrameCommands[C]
	// LatestFrame 当前最新帧号。
	LatestFrame uint64
	// FullReset 是否为全量恢复(指令已被覆盖,无法追帧)。
	// 此时 Commands 可能为空或不完整,客户端应直接用 Snapshot 覆盖。
	FullReset bool
}

// FrameCommands 一帧的指令。
type FrameCommands[C any] struct {
	Frame    uint64
	Commands []C
}

// Recover 计算从 since 帧断线到现在的恢复数据。
//
// 策略:
//  1. 找到 >= since 的最近快照作为起点(如果有更早的,用更早的以覆盖更多帧);
//  2. 收集快照之后到 latestFrame 之间的指令流;
//  3. 如果指令流不完整(环形缓冲已覆盖),标记 FullReset。
//
// 返回 false 表示没有任何快照可用(Log 为空)。
func (l *Log[S, C]) Recover(since uint64) (Recovery[S, C], bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.snapCount == 0 {
		return Recovery[S, C]{}, false
	}

	// 找最优快照:帧号 <= since 的最近快照;如果都 > since,取最小的
	bestIdx := -1
	var bestFrame uint64
	for i := range l.snapCount {
		idx := (l.snapHead - 1 - i + l.snapDepth) % l.snapDepth
		sf := l.snaps[idx].frame
		if sf <= since {
			if bestIdx == -1 || sf > bestFrame {
				bestIdx = idx
				bestFrame = sf
			}
		}
	}
	if bestIdx == -1 {
		// 所有快照都在 since 之后,取最老的快照
		bestIdx = (l.snapHead - l.snapCount + l.snapDepth) % l.snapDepth
		bestFrame = l.snaps[bestIdx].frame
	}

	snap := l.snaps[bestIdx]

	// 收集 snap.frame < frame <= latestFrame 的指令
	var commands []FrameCommands[C]
	fullReset := false

	if l.cmdCount > 0 {
		oldest := l.cmds[(l.cmdHead-l.cmdCount+l.cmdDepth)%l.cmdDepth].frame
		if oldest > snap.frame+1 {
			fullReset = true
		}

		// 按帧号升序收集
		for i := range l.cmdCount {
			idx := (l.cmdHead - l.cmdCount + i + l.cmdDepth) % l.cmdDepth
			entry := l.cmds[idx]
			if entry.frame <= snap.frame {
				continue
			}
			commands = append(commands, FrameCommands[C]{
				Frame:    entry.frame,
				Commands: entry.cmds,
			})
		}
	}

	return Recovery[S, C]{
		Snapshot:      snap.state,
		SnapshotFrame: snap.frame,
		Commands:      commands,
		LatestFrame:   l.latestFrame,
		FullReset:     fullReset,
	}, true
}

// LatestFrame 返回最新记录的帧号。
func (l *Log[S, C]) LatestFrame() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.latestFrame
}

// CmdCount 返回缓冲中的指令帧数。
func (l *Log[S, C]) CmdCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cmdCount
}

// SnapCount 返回缓冲中的快照数。
func (l *Log[S, C]) SnapCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.snapCount
}
