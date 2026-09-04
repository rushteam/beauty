package voxel

import "sync"

// ChunkSnapshot 单个区块快照。
type ChunkSnapshot struct {
	Pos      ChunkPos
	Revision uint64
	Blocks   [blocksPerChunk]BlockID
}

// WorldSnapshot 世界快照:多区块的一致性切面。
type WorldSnapshot struct {
	Revision uint64
	Chunks   []ChunkSnapshot
}

// TakeSnapshot 对 World 拍摄一致性快照。
// mutation 可选,如果提供则记录 Revision;否则取所有 Chunk Revision 的最大值。
func TakeSnapshot(w *World, mutation *Mutation) WorldSnapshot {
	var rev uint64
	if mutation != nil {
		rev = mutation.Revision()
	}

	positions := w.LoadedChunks()
	snap := WorldSnapshot{
		Revision: rev,
		Chunks:   make([]ChunkSnapshot, 0, len(positions)),
	}

	for _, pos := range positions {
		c := w.Chunk(pos)
		if c == nil {
			continue
		}
		cs := ChunkSnapshot{
			Pos:      pos,
			Revision: c.Revision(),
			Blocks:   c.Blocks(),
		}
		if rev == 0 && cs.Revision > snap.Revision {
			snap.Revision = cs.Revision
		}
		snap.Chunks = append(snap.Chunks, cs)
	}
	return snap
}

// RestoreSnapshot 从快照恢复 World 状态。会清除现有区块并加载快照中的所有区块。
func RestoreSnapshot(w *World, snap WorldSnapshot) {
	for _, pos := range w.LoadedChunks() {
		w.UnloadChunk(pos)
	}
	for _, cs := range snap.Chunks {
		c, _ := w.LoadChunk(cs.Pos)
		c.LoadBlocks(cs.Blocks[:])
	}
}

// SnapshotRing 世界快照的环形缓冲(类似 snapbuf.Ring,专用于体素世界)。
type SnapshotRing struct {
	mu    sync.RWMutex
	depth int
	buf   []snapEntry
	head  int
	count int
}

type snapEntry struct {
	revision uint64
	snap     WorldSnapshot
}

// NewSnapshotRing 创建快照环形缓冲。depth 为保留快照数(<=0 取 8)。
func NewSnapshotRing(depth int) *SnapshotRing {
	if depth <= 0 {
		depth = 8
	}
	return &SnapshotRing{depth: depth, buf: make([]snapEntry, depth)}
}

// Push 保存快照。
func (r *SnapshotRing) Push(snap WorldSnapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf[r.head] = snapEntry{revision: snap.Revision, snap: snap}
	r.head = (r.head + 1) % r.depth
	if r.count < r.depth {
		r.count++
	}
}

// Latest 返回最新快照。
func (r *SnapshotRing) Latest() (WorldSnapshot, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.count == 0 {
		return WorldSnapshot{}, false
	}
	idx := (r.head - 1 + r.depth) % r.depth
	return r.buf[idx].snap, true
}

// At 精确查找指定 Revision 的快照。
func (r *SnapshotRing) At(revision uint64) (WorldSnapshot, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for i := 0; i < r.count; i++ {
		idx := (r.head - 1 - i + r.depth) % r.depth
		if r.buf[idx].revision == revision {
			return r.buf[idx].snap, true
		}
	}
	return WorldSnapshot{}, false
}

// Nearest 返回 <= revision 的最近快照。
func (r *SnapshotRing) Nearest(revision uint64) (WorldSnapshot, uint64, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var (
		best    snapEntry
		found   bool
		bestGap uint64
	)
	for i := 0; i < r.count; i++ {
		idx := (r.head - 1 - i + r.depth) % r.depth
		e := r.buf[idx]
		if e.revision > revision {
			continue
		}
		gap := revision - e.revision
		if !found || gap < bestGap {
			best, bestGap, found = e, gap, true
		}
	}
	if !found {
		return WorldSnapshot{}, 0, false
	}
	return best.snap, best.revision, true
}

// Len 返回缓冲中的快照数。
func (r *SnapshotRing) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.count
}
