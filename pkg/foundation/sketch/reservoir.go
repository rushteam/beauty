package sketch

import (
	"math/rand/v2"
	"sync"
)

// Reservoir 蓄水池采样:从长度未知的流里等概率抽取至多 k 个元素(算法 R)。任意时刻已见 n 个元素时,
// 池中每个元素都以 k/n 的概率留存,且无需预先知道 n、无需缓存整条流。适合日志/trace 采样、
// 限流下采样等"边流边采"场景。并发安全。
type Reservoir[T any] struct {
	mu    sync.Mutex
	k     int
	n     int // 已见元素总数
	items []T
}

// NewReservoir 创建容量为 k 的蓄水池(k<=0 会被夹到 1)。
func NewReservoir[T any](k int) *Reservoir[T] {
	if k < 1 {
		k = 1
	}
	return &Reservoir[T]{k: k, items: make([]T, 0, k)}
}

// Add 送入一个元素(算法 R):未满直接放入;已满则以 k/n 概率替换一个随机位置。
func (r *Reservoir[T]) Add(x T) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.n++
	if len(r.items) < r.k {
		r.items = append(r.items, x)
		return
	}
	if j := rand.IntN(r.n); j < r.k { // 概率 k/n
		r.items[j] = x
	}
}

// Sample 返回当前样本的拷贝(数量 <= k)。
func (r *Reservoir[T]) Sample() []T {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]T, len(r.items))
	copy(out, r.items)
	return out
}

// Count 返回已见元素总数 n。
func (r *Reservoir[T]) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n
}

// Len 返回当前样本数(<= k)。
func (r *Reservoir[T]) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.items)
}

// Reset 清空,可复用。
func (r *Reservoir[T]) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = r.items[:0]
	r.n = 0
}
