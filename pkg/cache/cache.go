// Package cache 提供有界内存缓存(带淘汰)与防击穿加载器,是 web/API 读路径的常用原语:
//
//   - Cache 接口:并发安全的 Get/Set/Delete/Len,容量有界、满了自动淘汰;
//   - LRU:经典最近最少使用(container/list),小巧、可预测;
//   - TinyLFU(W-TinyLFU):窗口 LRU + 主 SLRU + Count-Min 频率准入,命中率接近最优、抗扫描
//     (一次性大量冷 key 不会冲垮热点),内存开销极小(每 key 4-bit 频率计数);
//   - Loader:Cache + singleflight + TTL + 负缓存,一站式解决缓存穿透/击穿/雪崩(见 loader.go)。
//
// 边界(机制而非策略):容量、TTL、用 LRU 还是 TinyLFU、回源逻辑都由调用方定。纯标准库,泛型键值。
package cache

import (
	"container/list"
	"sync"
)

// Cache 是并发安全、容量有界的缓存。达到容量后写入会触发淘汰。
type Cache[K comparable, V any] interface {
	Get(key K) (V, bool)
	Set(key K, value V)
	Delete(key K)
	Len() int
}

// --- LRU ---

type lruEntry[K comparable, V any] struct {
	key K
	val V
}

// LRU 是经典的最近最少使用缓存:容量满时淘汰最久未使用的项。并发安全。
type LRU[K comparable, V any] struct {
	mu    sync.Mutex
	cap   int
	ll    *list.List // 队首=最近使用,队尾=最久未使用
	items map[K]*list.Element
}

// NewLRU 创建容量为 capacity 的 LRU(capacity<=0 视为容量 1)。
func NewLRU[K comparable, V any](capacity int) *LRU[K, V] {
	if capacity <= 0 {
		capacity = 1
	}
	return &LRU[K, V]{cap: capacity, ll: list.New(), items: make(map[K]*list.Element, capacity)}
}

// Get 取值并将其标记为最近使用。
func (c *LRU[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		return el.Value.(*lruEntry[K, V]).val, true
	}
	var zero V
	return zero, false
}

// Set 写入或更新;超出容量则淘汰队尾(最久未使用)。
func (c *LRU[K, V]) Set(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		el.Value.(*lruEntry[K, V]).val = value
		c.ll.MoveToFront(el)
		return
	}
	el := c.ll.PushFront(&lruEntry[K, V]{key: key, val: value})
	c.items[key] = el
	if c.ll.Len() > c.cap {
		c.removeOldest()
	}
}

// Delete 删除一个键。
func (c *LRU[K, V]) Delete(key K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.removeElement(el)
	}
}

// Len 返回当前元素数。
func (c *LRU[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}

func (c *LRU[K, V]) removeOldest() {
	if el := c.ll.Back(); el != nil {
		c.removeElement(el)
	}
}

func (c *LRU[K, V]) removeElement(el *list.Element) {
	c.ll.Remove(el)
	delete(c.items, el.Value.(*lruEntry[K, V]).key)
}

var _ Cache[string, int] = (*LRU[string, int])(nil)
