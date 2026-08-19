// Package bitmap 提供位图:用 1 bit 表示一个布尔状态,极省内存的大规模标记与集合运算。
//
// 典型:
//   - 签到:每天一张 bitmap,第 i 位=用户 i 是否签到——1000 万用户仅需 ~1.25MB/天;
//     多天 bitmap 做 And 可算"连续签到",Count 算"当日签到数";
//   - 去重 / 存在性标记:第 i 位=ID i 是否出现过(ID 稠密时比 map 省几十倍内存);
//   - 权限位 / 特征位:一个整数装不下时用 bitmap 表示大量开关。
//
// 与 pkg/utils/bloom 的区别:bloom 是概率型(有假阳性、省内存、不可枚举),用于
// "可能存在"的快速判断;bitmap 是精确型(位与 ID 一一对应,可精确 Count/枚举),
// 用于 ID 稠密、需要精确计数与集合运算的场景。
//
// 底层 []uint64,按需增长。非并发安全(读多写少可在上层加锁;或每个写者独占一张)。
// 零值可用(空位图),也可用 New 预分配。
package bitmap

import "math/bits"

// Bitmap 是一个可增长的位图。零值可用。非并发安全。
type Bitmap struct {
	words []uint64
}

// New 创建一个至少能容纳 [0, n) 位的位图(预分配,避免增长时的多次分配)。
func New(n int) *Bitmap {
	if n < 0 {
		n = 0
	}
	return &Bitmap{words: make([]uint64, (n+63)/64)}
}

// grow 确保能容纳下标 i。
func (b *Bitmap) grow(i int) {
	need := i/64 + 1
	if need > len(b.words) {
		w := make([]uint64, need)
		copy(w, b.words)
		b.words = w
	}
}

// Set 置位 i(设为 1)。i 越界会自动增长。i<0 忽略。
func (b *Bitmap) Set(i int) {
	if i < 0 {
		return
	}
	b.grow(i)
	b.words[i/64] |= 1 << (uint(i) % 64)
}

// Clear 清位 i(设为 0)。越界/负数忽略。
func (b *Bitmap) Clear(i int) {
	if i < 0 || i/64 >= len(b.words) {
		return
	}
	b.words[i/64] &^= 1 << (uint(i) % 64)
}

// Test 返回位 i 是否为 1。越界/负数返回 false。
func (b *Bitmap) Test(i int) bool {
	if i < 0 || i/64 >= len(b.words) {
		return false
	}
	return b.words[i/64]&(1<<(uint(i)%64)) != 0
}

// Flip 翻转位 i,返回翻转后的值。越界自动增长。
func (b *Bitmap) Flip(i int) bool {
	if i < 0 {
		return false
	}
	b.grow(i)
	b.words[i/64] ^= 1 << (uint(i) % 64)
	return b.Test(i)
}

// Count 返回置为 1 的位总数(popcount,O(words))。
func (b *Bitmap) Count() int {
	var n int
	for _, w := range b.words {
		n += bits.OnesCount64(w)
	}
	return n
}

// And 就地与另一位图求交(自身 = 自身 ∩ other),返回自身以便链式。
func (b *Bitmap) And(other *Bitmap) *Bitmap {
	for i := range b.words {
		if i < len(other.words) {
			b.words[i] &= other.words[i]
		} else {
			b.words[i] = 0 // 超出 other 的部分交集为 0
		}
	}
	return b
}

// Or 就地与另一位图求并(自身 = 自身 ∪ other),返回自身。
func (b *Bitmap) Or(other *Bitmap) *Bitmap {
	if len(other.words) > len(b.words) {
		b.grow(len(other.words)*64 - 1)
	}
	for i := range other.words {
		b.words[i] |= other.words[i]
	}
	return b
}

// AndNot 就地求差(自身 = 自身 \ other,清掉 other 中为 1 的位),返回自身。
func (b *Bitmap) AndNot(other *Bitmap) *Bitmap {
	for i := range b.words {
		if i < len(other.words) {
			b.words[i] &^= other.words[i]
		}
	}
	return b
}

// Slice 返回所有置 1 位的下标(升序)。用于枚举(如"当日签到的用户 ID 列表")。
func (b *Bitmap) Slice() []int {
	out := make([]int, 0, b.Count())
	for wi, w := range b.words {
		for w != 0 {
			bit := bits.TrailingZeros64(w)
			out = append(out, wi*64+bit)
			w &= w - 1 // 清掉最低位的 1
		}
	}
	return out
}

// Clone 返回位图的深拷贝。
func (b *Bitmap) Clone() *Bitmap {
	w := make([]uint64, len(b.words))
	copy(w, b.words)
	return &Bitmap{words: w}
}

// Reset 清空所有位(容量保留)。
func (b *Bitmap) Reset() {
	for i := range b.words {
		b.words[i] = 0
	}
}

// ===== 多日签到工具 =====

// ConsecutiveFromEnd 对一组"按时间顺序"的每日签到位图,返回用户 uid 从最后一天
// 往前数的连续签到天数(最后一天未签到则为 0)。days 顺序:days[len-1] 为最新一天。
//
// 例:days = [周一,周二,周三],uid 在周二、周三签到 → 从末尾连续 2 天。
func ConsecutiveFromEnd(days []*Bitmap, uid int) int {
	n := 0
	for i := len(days) - 1; i >= 0; i-- {
		if days[i].Test(uid) {
			n++
		} else {
			break
		}
	}
	return n
}
