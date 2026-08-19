package sketch

import "math"

// CountMin(Count-Min Sketch)用常量内存估计每个 key 的累计频率:d 行 × w 列计数器,
// 每个 key 在每行哈希到一个计数器并累加,查询取各行最小值。只会**高估、不会低估**
// (碰撞使计数偏大),适合"热 key / Top-N / 限流分桶"这类"宁可高估"的场景。并发不安全。
type CountMin struct {
	d, w   uint32
	counts [][]uint64
}

// NewCountMin 按 width×depth 直接创建。width/depth<=0 会被夹到 1。
func NewCountMin(width, depth int) *CountMin {
	if width < 1 {
		width = 1
	}
	if depth < 1 {
		depth = 1
	}
	c := &CountMin{d: uint32(depth), w: uint32(width)}
	c.counts = make([][]uint64, depth)
	for i := range c.counts {
		c.counts[i] = make([]uint64, width)
	}
	return c
}

// NewCountMinWithError 按误差目标创建:估计值以概率 (1-delta) 不超过真实值 + epsilon*总量。
// width=ceil(e/epsilon),depth=ceil(ln(1/delta))。例如 epsilon=0.001,delta=0.01 → ~2718×5。
func NewCountMinWithError(epsilon, delta float64) *CountMin {
	if epsilon <= 0 {
		epsilon = 0.001
	}
	if delta <= 0 || delta >= 1 {
		delta = 0.01
	}
	width := int(math.Ceil(math.E / epsilon))
	depth := int(math.Ceil(math.Log(1 / delta)))
	return NewCountMin(width, depth)
}

// 双重散列派生每行位置。
func (c *CountMin) pos(h uint64, i uint32) uint32 {
	h2 := mix64(h) | 1
	return uint32((h + uint64(i)*h2) % uint64(c.w))
}

// Add 给 key 累加 n。
func (c *CountMin) Add(key string, n uint64) { c.AddHash(hashString(key), n) }

// AddHash 给已算好的哈希累加 n。
func (c *CountMin) AddHash(h, n uint64) {
	for i := uint32(0); i < c.d; i++ {
		c.counts[i][c.pos(h, i)] += n
	}
}

// Count 返回 key 的估计频率(各行最小值)。
func (c *CountMin) Count(key string) uint64 { return c.CountHash(hashString(key)) }

// CountHash 返回已算好哈希的估计频率。
func (c *CountMin) CountHash(h uint64) uint64 {
	min := ^uint64(0)
	for i := uint32(0); i < c.d; i++ {
		if v := c.counts[i][c.pos(h, i)]; v < min {
			min = v
		}
	}
	return min
}

// Reset 清零。
func (c *CountMin) Reset() {
	for i := range c.counts {
		row := c.counts[i]
		for j := range row {
			row[j] = 0
		}
	}
}
