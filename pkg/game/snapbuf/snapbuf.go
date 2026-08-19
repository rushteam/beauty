// Package snapbuf 提供固定深度的环形快照缓冲,用于延迟补偿/server rewind。
package snapbuf

import "sync"

// Ring 保存最近 depth 帧的快照(S 由业务定义,如 world 副本)。
type Ring[S any] struct {
	mu     sync.RWMutex
	depth  int
	buf    []entry[S]
	head   int
	count  int
	latest uint64
}

type entry[S any] struct {
	frame uint64
	snap  S
}

// New 创建深度为 depth 的环形缓冲(depth<=0 时取 64)。
func New[S any](depth int) *Ring[S] {
	if depth <= 0 {
		depth = 64
	}
	return &Ring[S]{depth: depth, buf: make([]entry[S], depth)}
}

// Push 写入 frame 对应的快照;frame 应单调递增。
func (r *Ring[S]) Push(frame uint64, snap S) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if frame > r.latest {
		r.latest = frame
	}
	r.buf[r.head] = entry[S]{frame: frame, snap: snap}
	r.head = (r.head + 1) % r.depth
	if r.count < r.depth {
		r.count++
	}
}

// At 精确查找 frame 对应的快照。
func (r *Ring[S]) At(frame uint64) (S, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for i := 0; i < r.count; i++ {
		idx := (r.head - 1 - i + r.depth) % r.depth
		if r.buf[idx].frame == frame {
			return r.buf[idx].snap, true
		}
	}
	var zero S
	return zero, false
}

// Nearest 返回 <= frame 的最近快照(用于 RTT 补偿)。
func (r *Ring[S]) Nearest(frame uint64) (S, uint64, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var (
		best    entry[S]
		found   bool
		bestGap uint64
	)
	for i := 0; i < r.count; i++ {
		idx := (r.head - 1 - i + r.depth) % r.depth
		e := r.buf[idx]
		if e.frame > frame {
			continue
		}
		gap := frame - e.frame
		if !found || gap < bestGap {
			best, bestGap, found = e, gap, true
		}
	}
	if !found {
		var zero S
		return zero, 0, false
	}
	return best.snap, best.frame, true
}

// Latest 返回最新帧号与快照。
func (r *Ring[S]) Latest() (frame uint64, snap S, ok bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.count == 0 {
		return 0, snap, false
	}
	idx := (r.head - 1 + r.depth) % r.depth
	return r.buf[idx].frame, r.buf[idx].snap, true
}

// Len 返回当前缓冲中的帧数(<= depth)。
func (r *Ring[S]) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.count
}
