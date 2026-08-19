// Package priority 提供泛型优先级队列:基于二叉堆实现,支持 Push/Pop/Peek/Update/Remove。
//
// 典型场景:
//   - 调度器:按优先级或截止时间取下一个任务
//   - 匹配系统:按等待时长优先出队
//   - 延迟队列:按触发时间排序
//   - 定时器:最近到期的在堆顶
//
// 提供两个变体:
//   - Queue[T]:非并发安全,零开销(适合单 goroutine 使用)
//   - SyncQueue[T]:内置 RWMutex 的并发安全版本
//
// 纯标准库、泛型。
package priority

import "sync"

// LessFunc 比较函数:返回 true 表示 a 优先级高于 b(应排在前面)。
type LessFunc[T any] func(a, b T) bool

// Queue 泛型优先级队列(二叉堆)。非并发安全。
type Queue[T any] struct {
	items []T
	less  LessFunc[T]
	index map[int]int // item ptr → heap index (仅 Update/Remove 使用，通过用户提供 key)
}

// New 创建优先级队列。less(a,b) 返回 true 表示 a 排在 b 前面(最小堆:用 <,最大堆:用 >)。
func New[T any](less LessFunc[T]) *Queue[T] {
	return &Queue[T]{
		less: less,
	}
}

// Len 返回队列长度。
func (q *Queue[T]) Len() int { return len(q.items) }

// Push 入队。
func (q *Queue[T]) Push(item T) {
	q.items = append(q.items, item)
	q.up(len(q.items) - 1)
}

// Pop 取出优先级最高的元素。队列为空时 panic。
func (q *Queue[T]) Pop() T {
	n := len(q.items)
	q.swap(0, n-1)
	q.down(0, n-1)
	item := q.items[n-1]
	var zero T
	q.items[n-1] = zero
	q.items = q.items[:n-1]
	return item
}

// Peek 查看优先级最高的元素(不出队)。队列为空时 panic。
func (q *Queue[T]) Peek() T {
	return q.items[0]
}

// PushPop 等价于 Push+Pop 但更高效(避免不必要的堆调整)。
func (q *Queue[T]) PushPop(item T) T {
	if len(q.items) > 0 && q.less(q.items[0], item) {
		item, q.items[0] = q.items[0], item
		q.down(0, len(q.items))
	}
	return item
}

// Remove 按索引删除元素。返回被删除的元素。
func (q *Queue[T]) Remove(i int) T {
	n := len(q.items)
	if i != n-1 {
		q.swap(i, n-1)
		q.down(i, n-1)
		q.up(i)
	}
	item := q.items[n-1]
	var zero T
	q.items[n-1] = zero
	q.items = q.items[:n-1]
	return item
}

// Update 通知队列第 i 个元素的优先级已变化,重新调整位置。
func (q *Queue[T]) Update(i int) {
	if !q.down(i, len(q.items)) {
		q.up(i)
	}
}

// Items 返回底层切片的副本(无序,堆序)。
func (q *Queue[T]) Items() []T {
	cp := make([]T, len(q.items))
	copy(cp, q.items)
	return cp
}

// At 返回索引 i 处的元素。
func (q *Queue[T]) At(i int) T { return q.items[i] }

func (q *Queue[T]) up(j int) {
	for {
		i := (j - 1) / 2
		if i == j || !q.less(q.items[j], q.items[i]) {
			break
		}
		q.swap(i, j)
		j = i
	}
}

func (q *Queue[T]) down(i0, n int) bool {
	i := i0
	for {
		j1 := 2*i + 1
		if j1 >= n || j1 < 0 {
			break
		}
		j := j1
		if j2 := j1 + 1; j2 < n && q.less(q.items[j2], q.items[j1]) {
			j = j2
		}
		if !q.less(q.items[j], q.items[i]) {
			break
		}
		q.swap(i, j)
		i = j
	}
	return i > i0
}

func (q *Queue[T]) swap(i, j int) {
	q.items[i], q.items[j] = q.items[j], q.items[i]
}

// SyncQueue 并发安全的优先级队列。
type SyncQueue[T any] struct {
	mu sync.Mutex
	q  *Queue[T]
}

// NewSync 创建并发安全的优先级队列。
func NewSync[T any](less LessFunc[T]) *SyncQueue[T] {
	return &SyncQueue[T]{q: New(less)}
}

// Len 返回队列长度。
func (sq *SyncQueue[T]) Len() int {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	return sq.q.Len()
}

// Push 入队。
func (sq *SyncQueue[T]) Push(item T) {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	sq.q.Push(item)
}

// Pop 取出优先级最高的元素。队列为空返回 zero value 和 false。
func (sq *SyncQueue[T]) Pop() (T, bool) {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	if sq.q.Len() == 0 {
		var zero T
		return zero, false
	}
	return sq.q.Pop(), true
}

// Peek 查看优先级最高的元素。队列为空返回 zero value 和 false。
func (sq *SyncQueue[T]) Peek() (T, bool) {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	if sq.q.Len() == 0 {
		var zero T
		return zero, false
	}
	return sq.q.Peek(), true
}

// PushPop 等价于 Push+Pop。
func (sq *SyncQueue[T]) PushPop(item T) T {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	return sq.q.PushPop(item)
}
