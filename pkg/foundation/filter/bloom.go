package filter

import (
	"math"
	"math/bits"
)

// BloomFilter 是纯内存布隆过滤器。零值不可用,用 NewBloom 构造。非并发安全。
//
// 原理:m 个 bit + k 个哈希函数。Add 时把元素的 k 个哈希位置全部置 1;Test 时只要
// 有任一位为 0,则元素**一定不存在**;全为 1 则**可能存在**(可能是别的元素凑巧把这些
// 位都置 1 了,即假阳性)。绝不假阴性。
//
// 用双重散列 h_i = h1 + i*h2 (mod m) 从两个哈希派生 k 个位置(Kirsch-Mitzenmacher),
// 避免维护 k 个独立哈希函数,误判率与真独立哈希基本一致。
type BloomFilter struct {
	bits []uint64 // 位存储,每 uint64 存 64 个 bit
	m    uint64   // 位总数
	k    uint64   // 哈希函数个数
	n    uint64   // 已插入元素个数(用于估计填充率/误判率)
}

// NewBloom 按"预期元素数 n 与目标误判率 p"创建过滤器,自动推导最优 m 与 k:
//
//	m = ceil(-n*ln(p) / (ln2)^2)   位数
//	k = round(m/n * ln2)           哈希数
//
// n<=0 视为 1;p 会被夹到 (0,1) 开区间(默认 0.01)。
func NewBloom(n int, p float64) *BloomFilter {
	if n <= 0 {
		n = 1
	}
	if p <= 0 || p >= 1 {
		p = 0.01
	}
	ln2 := math.Ln2
	m := uint64(math.Ceil(-float64(n) * math.Log(p) / (ln2 * ln2)))
	if m < 1 {
		m = 1
	}
	k := uint64(math.Round(float64(m) / float64(n) * ln2))
	if k < 1 {
		k = 1
	}
	return &BloomFilter{
		bits: make([]uint64, (m+63)/64),
		m:    m,
		k:    k,
	}
}

// NewBloomRaw 直接指定位数 m 与哈希数 k(高级用法,一般用 NewBloom)。m/k<1 夹到 1。
func NewBloomRaw(m, k int) *BloomFilter {
	if m < 1 {
		m = 1
	}
	if k < 1 {
		k = 1
	}
	return &BloomFilter{
		bits: make([]uint64, (m+63)/64),
		m:    uint64(m),
		k:    uint64(k),
	}
}

// positions 用双重散列派生第 i 个位下标。
func (f *BloomFilter) hashes(b []byte) (h1, h2 uint64) {
	h1 = hashBytes(b)
	h2 = mix64(h1) | 1 // 保证 h2 为奇数,配合模运算遍历更均匀
	return
}

// Add 插入元素。
func (f *BloomFilter) Add(data []byte) {
	h1, h2 := f.hashes(data)
	for i := uint64(0); i < f.k; i++ {
		pos := (h1 + i*h2) % f.m
		f.bits[pos/64] |= 1 << (pos % 64)
	}
	f.n++
}

// AddString 插入字符串元素。
func (f *BloomFilter) AddString(s string) { f.Add([]byte(s)) }

// Test 判断元素是否可能存在:返回 false 表示**一定不存在**;true 表示**可能存在**(含假阳性)。
func (f *BloomFilter) Test(data []byte) bool {
	h1, h2 := f.hashes(data)
	for i := uint64(0); i < f.k; i++ {
		pos := (h1 + i*h2) % f.m
		if f.bits[pos/64]&(1<<(pos%64)) == 0 {
			return false
		}
	}
	return true
}

// TestString 判断字符串元素是否可能存在。
func (f *BloomFilter) TestString(s string) bool { return f.Test([]byte(s)) }

// TestAndAdd 先判断再插入,返回插入前是否"可能已存在"。用于"首次出现即处理"的去重场景。
func (f *BloomFilter) TestAndAdd(data []byte) bool {
	h1, h2 := f.hashes(data)
	existed := true
	for i := uint64(0); i < f.k; i++ {
		pos := (h1 + i*h2) % f.m
		w := &f.bits[pos/64]
		mask := uint64(1) << (pos % 64)
		if *w&mask == 0 {
			existed = false
			*w |= mask
		}
	}
	f.n++
	return existed
}

// Len 返回已插入元素个数(每次 Add 计数,不去重)。
func (f *BloomFilter) Len() uint64 { return f.n }

// Cap 返回位总数 m。
func (f *BloomFilter) Cap() uint64 { return f.m }

// K 返回哈希函数个数。
func (f *BloomFilter) K() uint64 { return f.k }

// FillRatio 返回当前置 1 位的比例([0,1])。接近 1 时误判率急剧上升。
func (f *BloomFilter) FillRatio() float64 {
	var set int
	for _, w := range f.bits {
		set += bits.OnesCount64(w)
	}
	return float64(set) / float64(f.m)
}

// EstimatedFalsePositiveRate 基于当前填充率估计的假阳性率:(设置位比例)^k。
func (f *BloomFilter) EstimatedFalsePositiveRate() float64 {
	return math.Pow(f.FillRatio(), float64(f.k))
}

// Reset 清空所有位与计数(容量保留)。
func (f *BloomFilter) Reset() {
	for i := range f.bits {
		f.bits[i] = 0
	}
	f.n = 0
}
