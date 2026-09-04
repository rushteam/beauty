package voxel

import "sync"

// BlockChange 记录单个方块变更。
type BlockChange struct {
	Pos      BlockPos
	OldBlock BlockID
	NewBlock BlockID
}

// Mutation 记录一帧内的方块变更,带递增 Revision。
//
// 用法:每个 game tick 开始时调用 Begin,tick 内通过 Record 记录所有变更,
// tick 结束时调用 Commit 取走本帧变更并递增 Revision。
//
// 结合 World 使用:先 World.SetBlock,再 Mutation.Record。
// 结合 gameloop 使用:OnTick 内收集 changes,输出 CommittedMutation 作为增量。
type Mutation struct {
	mu       sync.Mutex
	pending  []BlockChange
	revision uint64
}

// NewMutation 创建变更追踪器。
func NewMutation() *Mutation {
	return &Mutation{}
}

// Revision 返回当前 Revision(从 0 开始,每 Commit 递增)。
func (m *Mutation) Revision() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.revision
}

// Record 记录一个方块变更。
func (m *Mutation) Record(change BlockChange) {
	m.mu.Lock()
	m.pending = append(m.pending, change)
	m.mu.Unlock()
}

// RecordSet 在 World 上执行 SetBlock 并自动记录变更,返回旧方块。
func (m *Mutation) RecordSet(w *World, pos BlockPos, block BlockID) BlockID {
	old := w.SetBlock(pos, block)
	if old != block {
		m.Record(BlockChange{Pos: pos, OldBlock: old, NewBlock: block})
	}
	return old
}

// Commit 取走本帧所有待处理变更并递增 Revision。
// 返回 CommittedMutation 用于增量同步。无变更时返回 nil。
func (m *Mutation) Commit() *CommittedMutation {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.pending) == 0 {
		return nil
	}
	m.revision++
	cm := &CommittedMutation{
		Revision: m.revision,
		Changes:  m.pending,
	}
	m.pending = nil
	return cm
}

// Pending 返回当前待提交变更数。
func (m *Mutation) Pending() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.pending)
}

// CommittedMutation 一帧已提交的变更(不可变)。
type CommittedMutation struct {
	Revision uint64
	Changes  []BlockChange
}

// MutationLog 保存最近 N 帧的已提交变更(环形缓冲),用于增量同步/断线重连。
type MutationLog struct {
	mu    sync.RWMutex
	depth int
	buf   []CommittedMutation
	head  int
	count int
}

// NewMutationLog 创建变更日志,depth 为保留帧数(<=0 取 128)。
func NewMutationLog(depth int) *MutationLog {
	if depth <= 0 {
		depth = 128
	}
	return &MutationLog{depth: depth, buf: make([]CommittedMutation, depth)}
}

// Append 追加一帧变更。
func (l *MutationLog) Append(cm CommittedMutation) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf[l.head] = cm
	l.head = (l.head + 1) % l.depth
	if l.count < l.depth {
		l.count++
	}
}

// Since 返回 afterRevision 之后的所有变更(用于断线重连追赶)。
// 如果请求的 revision 过旧(已被覆盖导致存在间隙),返回 (nil, false),
// 调用方应改为全量同步。
func (l *MutationLog) Since(afterRevision uint64) ([]CommittedMutation, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.count == 0 {
		return nil, true
	}
	// 检查最旧的保留条目:如果 afterRevision 早于最旧条目的前一个 revision,
	// 说明存在间隙(中间有变更已被覆盖),返回 false 让调用方全量同步。
	oldest := l.buf[(l.head-l.count+l.depth)%l.depth]
	if afterRevision+1 < oldest.Revision {
		return nil, false
	}
	var out []CommittedMutation
	for i := 0; i < l.count; i++ {
		idx := (l.head - l.count + i + l.depth) % l.depth
		cm := l.buf[idx]
		if cm.Revision > afterRevision {
			out = append(out, cm)
		}
	}
	return out, true
}

// Latest 返回最新 Revision。无记录时返回 0。
func (l *MutationLog) Latest() uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.count == 0 {
		return 0
	}
	idx := (l.head - 1 + l.depth) % l.depth
	return l.buf[idx].Revision
}

// Len 返回当前日志中的帧数。
func (l *MutationLog) Len() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.count
}
