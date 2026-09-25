package filter

import "math/rand"

const (
	bucketSize   = 4   // 每个桶的槽位数(经典取 4,负载因子可达 ~95%)
	maxKicks     = 500 // 插入时最大踢出次数,超出视为过载
	emptyFinger  = 0   // 指纹 0 保留为"空槽"
	defaultCount = 8   // numBuckets 下限
)

// CuckooFilter 是纯内存布谷鸟过滤器。零值不可用,用 NewCuckoo 构造。非并发安全。
//
// 相比布隆过滤器,它**支持删除**(Delete),且低负载时空间/查询局部性更好;代价是
// 高负载(接近满)时插入可能失败(Add 返回 false),需预留容量。
//
// 原理:每个元素取一个 1 字节指纹 fp 与两个候选桶 i1、i2 = i1 ^ hash(fp)(异或使得
// 由任一桶都能算出另一个,无需存原 key)。插入时若两桶都满,则随机踢出一个已有指纹
// 到它的另一候选桶,循环至多 maxKicks 次。查存只需检查两个桶里是否有该指纹。
type CuckooFilter struct {
	buckets    [][bucketSize]byte
	numBuckets uint // 桶数,2 的幂(便于用位与取模)
	mask       uint
	count      uint // 已插入元素个数
	rng        *rand.Rand
}

// NewCuckoo 创建可容纳约 capacity 个元素的过滤器(实际容量为向上取到 2 的幂的桶数×4)。
// capacity<=0 时用最小容量。
func NewCuckoo(capacity int) *CuckooFilter {
	nb := uint(defaultCount)
	if capacity > 0 {
		// 每个桶 bucketSize 个槽,按 ~容量/bucketSize 取桶数,再向上取到 2 的幂
		need := uint(capacity) / bucketSize
		for nb < need {
			nb <<= 1
		}
	}
	return &CuckooFilter{
		buckets:    make([][bucketSize]byte, nb),
		numBuckets: nb,
		mask:       nb - 1,
		rng:        rand.New(rand.NewSource(1)), // 固定种子:确定性(踢出选择),测试可复现
	}
}

// fingerprintAndIndex 从数据计算指纹(1..255)与主桶下标。
func (c *CuckooFilter) fingerprintAndIndex(data []byte) (fp byte, i1 uint) {
	h := hashBytes(data)
	fp = byte(h >> 56) // 取高 8 位作指纹,与低位定位相对独立
	if fp == emptyFinger {
		fp = 1
	}
	i1 = uint(h) & c.mask
	return
}

// altIndex 由一个桶与指纹算出另一候选桶。
func (c *CuckooFilter) altIndex(i uint, fp byte) uint {
	return (i ^ uint(mix64(uint64(fp)))) & c.mask
}

// Add 插入元素。返回 false 表示过滤器已过载(踢出次数耗尽),此时元素可能未被插入,
// 应扩容或换用更大容量重建。重复插入同一元素会占用多个槽(概率型不去重)。
func (c *CuckooFilter) Add(data []byte) bool {
	fp, i1 := c.fingerprintAndIndex(data)
	i2 := c.altIndex(i1, fp)
	if c.insertInto(i1, fp) || c.insertInto(i2, fp) {
		c.count++
		return true
	}
	// 两桶皆满:随机选一桶开始踢出
	i := i1
	if c.rng.Intn(2) == 1 {
		i = i2
	}
	for range maxKicks {
		slot := c.rng.Intn(bucketSize)
		fp, c.buckets[i][slot] = c.buckets[i][slot], fp // 踢出旧指纹,放入当前指纹
		i = c.altIndex(i, fp)                           // 被踢出者去它的另一候选桶
		if c.insertInto(i, fp) {
			c.count++
			return true
		}
	}
	// 过载:把最后被踢出的指纹放回(尽量不丢),但仍报告失败
	return false
}

// AddString 插入字符串元素。
func (c *CuckooFilter) AddString(s string) bool { return c.Add([]byte(s)) }

// insertInto 尝试把 fp 放进桶 i 的空槽,成功返回 true。
func (c *CuckooFilter) insertInto(i uint, fp byte) bool {
	b := &c.buckets[i]
	for j := 0; j < bucketSize; j++ {
		if b[j] == emptyFinger {
			b[j] = fp
			return true
		}
	}
	return false
}

// Contains 判断元素是否可能存在:false 表示**一定不存在**;true 表示**可能存在**(含假阳性)。
func (c *CuckooFilter) Contains(data []byte) bool {
	fp, i1 := c.fingerprintAndIndex(data)
	if c.hasFinger(i1, fp) {
		return true
	}
	i2 := c.altIndex(i1, fp)
	return c.hasFinger(i2, fp)
}

// ContainsString 判断字符串元素是否可能存在。
func (c *CuckooFilter) ContainsString(s string) bool { return c.Contains([]byte(s)) }

// hasFinger 桶 i 是否含指纹 fp。
func (c *CuckooFilter) hasFinger(i uint, fp byte) bool {
	b := &c.buckets[i]
	for j := 0; j < bucketSize; j++ {
		if b[j] == fp {
			return true
		}
	}
	return false
}

// Delete 删除元素:命中(从任一候选桶清掉一个匹配指纹)返回 true。
// 注意:仅当元素确曾 Add 过才应 Delete;删除从未插入但假阳性命中的元素会破坏其他元素的判存。
func (c *CuckooFilter) Delete(data []byte) bool {
	fp, i1 := c.fingerprintAndIndex(data)
	if c.deleteFrom(i1, fp) {
		c.count--
		return true
	}
	i2 := c.altIndex(i1, fp)
	if c.deleteFrom(i2, fp) {
		c.count--
		return true
	}
	return false
}

// DeleteString 删除字符串元素。
func (c *CuckooFilter) DeleteString(s string) bool { return c.Delete([]byte(s)) }

// deleteFrom 从桶 i 清掉一个匹配指纹。
func (c *CuckooFilter) deleteFrom(i uint, fp byte) bool {
	b := &c.buckets[i]
	for j := 0; j < bucketSize; j++ {
		if b[j] == fp {
			b[j] = emptyFinger
			return true
		}
	}
	return false
}

// Len 返回已插入元素个数。
func (c *CuckooFilter) Len() uint { return c.count }

// Cap 返回总槽位数(numBuckets × bucketSize)。
func (c *CuckooFilter) Cap() uint { return c.numBuckets * bucketSize }

// LoadFactor 返回当前负载因子([0,1]):count / Cap。接近 1 时插入更易失败。
func (c *CuckooFilter) LoadFactor() float64 {
	return float64(c.count) / float64(c.Cap())
}

// Reset 清空所有桶与计数(容量保留)。
func (c *CuckooFilter) Reset() {
	for i := range c.buckets {
		c.buckets[i] = [bucketSize]byte{}
	}
	c.count = 0
}
