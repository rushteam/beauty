// Package ringbuffer 提供定长环形缓冲:只保留最近 N 条,写满后覆盖最旧的,
// O(1) 追加。泛型 Ring[T]。
//
// 典型:直播间"最近 50 条弹幕"、玩家"最近 20 场战绩"、"最近 100 行滚动日志"、
// 监控采样窗口。这类"只关心最近若干条、旧的自动丢弃"的需求,用 slice 手撸要反复
// 裁剪且容易写错边界,本包封装成固定内存、无分配增长的环。
//
// 提供两个类型:
//   - Ring[T]:非并发安全,零开销,单 goroutine 或调用方自行加锁时用;
//   - SyncRing[T]:内置 RWMutex 的并发安全版,读多写少场景直接用。
//
// 容量固定(构造时定),永不扩容;追加满后覆盖最旧元素。零值不可用,用 New 构造。
package ringbuffer

import "sync"

// Ring 定长环形缓冲(非并发安全)。零值不可用,用 New 构造。
type Ring[T any] struct {
	buf   []T
	head  int // 下一个写入位置
	count int // 当前元素数(<= cap)
}

// New 创建容量为 capacity 的环形缓冲。capacity<=0 会被置为 1。
func New[T any](capacity int) *Ring[T] {
	if capacity <= 0 {
		capacity = 1
	}
	return &Ring[T]{buf: make([]T, capacity)}
}

// Push 追加一个元素;缓冲已满则覆盖最旧的那个。
func (r *Ring[T]) Push(v T) {
	r.buf[r.head] = v
	r.head = (r.head + 1) % len(r.buf)
	if r.count < len(r.buf) {
		r.count++
	}
}

// Len 返回当前元素数。
func (r *Ring[T]) Len() int { return r.count }

// Cap 返回容量。
func (r *Ring[T]) Cap() int { return len(r.buf) }

// Full 返回是否已写满(此后 Push 会覆盖最旧)。
func (r *Ring[T]) Full() bool { return r.count == len(r.buf) }

// oldest 返回最旧元素的物理下标(count>0 时有效)。
func (r *Ring[T]) oldest() int {
	if r.count < len(r.buf) {
		return 0 // 还没绕回,最旧在 0
	}
	return r.head // 已满,head 指向的即最旧
}

// Slice 按从旧到新的顺序返回当前所有元素的拷贝(不含未填充的空位)。
func (r *Ring[T]) Slice() []T {
	out := make([]T, r.count)
	start := r.oldest()
	for i := range r.count {
		out[i] = r.buf[(start+i)%len(r.buf)]
	}
	return out
}

// Recent 返回最近 n 条(从新到旧)。n 超过当前元素数则返回全部(仍从新到旧)。
func (r *Ring[T]) Recent(n int) []T {
	if n <= 0 {
		return nil
	}
	if n > r.count {
		n = r.count
	}
	out := make([]T, n)
	// 最新元素在 head-1,依次往回。
	for i := range n {
		idx := (r.head - 1 - i + len(r.buf)*2) % len(r.buf)
		out[i] = r.buf[idx]
	}
	return out
}

// Newest 返回最新元素;为空返回零值 + false。
func (r *Ring[T]) Newest() (T, bool) {
	var zero T
	if r.count == 0 {
		return zero, false
	}
	return r.buf[(r.head-1+len(r.buf))%len(r.buf)], true
}

// Oldest 返回最旧元素;为空返回零值 + false。
func (r *Ring[T]) Oldest() (T, bool) {
	var zero T
	if r.count == 0 {
		return zero, false
	}
	return r.buf[r.oldest()], true
}

// Clear 清空(容量不变)。
func (r *Ring[T]) Clear() {
	var zero T
	for i := range r.buf {
		r.buf[i] = zero // 清引用,助 GC
	}
	r.head = 0
	r.count = 0
}

// ===== 并发安全版 =====

// SyncRing 是 Ring 的并发安全封装(RWMutex)。零值不可用,用 NewSync 构造。
type SyncRing[T any] struct {
	mu sync.RWMutex
	r  *Ring[T]
}

// NewSync 创建并发安全的环形缓冲。
func NewSync[T any](capacity int) *SyncRing[T] {
	return &SyncRing[T]{r: New[T](capacity)}
}

// Push 追加(写锁)。
func (s *SyncRing[T]) Push(v T) {
	s.mu.Lock()
	s.r.Push(v)
	s.mu.Unlock()
}

// Slice 从旧到新的拷贝(读锁)。
func (s *SyncRing[T]) Slice() []T {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.r.Slice()
}

// Recent 最近 n 条,从新到旧(读锁)。
func (s *SyncRing[T]) Recent(n int) []T {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.r.Recent(n)
}

// Newest 最新元素(读锁)。
func (s *SyncRing[T]) Newest() (T, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.r.Newest()
}

// Len 当前元素数(读锁)。
func (s *SyncRing[T]) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.r.Len()
}

// Cap 容量。
func (s *SyncRing[T]) Cap() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.r.Cap()
}

// Clear 清空(写锁)。
func (s *SyncRing[T]) Clear() {
	s.mu.Lock()
	s.r.Clear()
	s.mu.Unlock()
}
