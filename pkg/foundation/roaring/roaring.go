// Package roaring 提供 Roaring Bitmap(压缩位图):存储海量 uint32 整数集合,在「稀疏」与
// 「稠密」两种分布下都接近最优内存。它是「用户标签/圈选、布尔列索引、去重 ID 集、集合运算」
// 的工业标配,远比朴素位图(pkg/foundation/bitmap)省内存——朴素位图为 [0, maxID) 全量开位,
// ID 稀疏时极度浪费;Roaring 只为真正出现的高 16 位分桶分配容器。
//
// 结构:把 uint32 拆成高 16 位(桶 key)与低 16 位(桶内值)。每个桶是一个 65536 值域的容器,
// 按该桶的基数自适应选表示:
//   - 数组容器(array):排序的 []uint16,适合稀疏桶(<=4096 个值),内存 = 2 字节/值;
//   - 位图容器(bitmap):1024×uint64 定长位图,适合稠密桶,内存恒 8KB,查询 O(1)。
//
// 超过阈值自动从数组升级为位图。桶按 key 有序存放,便于区间与集合运算。
//
// 集合运算提供并(Or)、交(And)、差(AndNot),free 函数返回新集合,方法版就地修改。
// 并发不安全(与本仓其它数据结构一致,按需在外层加锁)。零值可用(等价空集),也可用 New。
package roaring

import "sort"

const arrayThreshold = 4096 // 数组容器升级为位图容器的基数阈值

// Bitmap 是一个 uint32 集合。按高 16 位分桶,keys 与 containers 下标一一对应且按 key 升序。
type Bitmap struct {
	keys       []uint16
	containers []*container
}

// New 创建空位图。
func New() *Bitmap { return &Bitmap{} }

// Of 用给定值构造位图。
func Of(values ...uint32) *Bitmap {
	b := &Bitmap{}
	b.AddMany(values...)
	return b
}

func hi(v uint32) uint16 { return uint16(v >> 16) }
func lo(v uint32) uint16 { return uint16(v) }

// locate 返回 key 对应容器的下标与是否存在(二分)。
func (b *Bitmap) locate(key uint16) (int, bool) {
	i := sort.Search(len(b.keys), func(i int) bool { return b.keys[i] >= key })
	return i, i < len(b.keys) && b.keys[i] == key
}

// Add 加入一个值,返回是否为新增(原本不存在)。
func (b *Bitmap) Add(v uint32) bool {
	key := hi(v)
	i, ok := b.locate(key)
	if !ok {
		c := &container{}
		// 在下标 i 处插入,保持 keys 有序。
		b.keys = append(b.keys, 0)
		copy(b.keys[i+1:], b.keys[i:])
		b.keys[i] = key
		b.containers = append(b.containers, nil)
		copy(b.containers[i+1:], b.containers[i:])
		b.containers[i] = c
		c.add(lo(v))
		return true
	}
	return b.containers[i].add(lo(v))
}

// AddMany 批量加入。
func (b *Bitmap) AddMany(values ...uint32) {
	for _, v := range values {
		b.Add(v)
	}
}

// Contains 判断值是否在集合中。
func (b *Bitmap) Contains(v uint32) bool {
	i, ok := b.locate(hi(v))
	return ok && b.containers[i].contains(lo(v))
}

// Remove 删除一个值,返回是否原本存在。空容器会被回收。
func (b *Bitmap) Remove(v uint32) bool {
	i, ok := b.locate(hi(v))
	if !ok {
		return false
	}
	removed := b.containers[i].remove(lo(v))
	if removed && b.containers[i].cardinality() == 0 {
		b.keys = append(b.keys[:i], b.keys[i+1:]...)
		b.containers = append(b.containers[:i], b.containers[i+1:]...)
	}
	return removed
}

// Len 返回集合基数(元素个数)。
func (b *Bitmap) Len() uint64 {
	var n uint64
	for _, c := range b.containers {
		n += uint64(c.cardinality())
	}
	return n
}

// IsEmpty 是否空集。
func (b *Bitmap) IsEmpty() bool { return len(b.keys) == 0 }

// Range 按升序遍历所有值。fn 返回 false 可提前停止。
func (b *Bitmap) Range(fn func(v uint32) bool) {
	for i, key := range b.keys {
		base := uint32(key) << 16
		stop := false
		b.containers[i].each(func(l uint16) bool {
			if !fn(base | uint32(l)) {
				stop = true
				return false
			}
			return true
		})
		if stop {
			return
		}
	}
}

// ToArray 返回升序的全部值(大集合慎用,内存与基数同阶)。
func (b *Bitmap) ToArray() []uint32 {
	out := make([]uint32, 0, b.Len())
	b.Range(func(v uint32) bool {
		out = append(out, v)
		return true
	})
	return out
}

// RunOptimize 对每个桶尝试改用游程(run)表示:当连续区间多时,游程比数组/位图更省内存,
// 也让序列化更小。对「大段连续 ID」(如 1..1_000_000 全选)效果显著。幂等。
func (b *Bitmap) RunOptimize() {
	for _, c := range b.containers {
		if c.isRun() {
			continue
		}
		cur := c.arrayBytes()
		if c.isBitmap() {
			cur = c.bitmapBytes()
		}
		if c.runBytes() < cur {
			c.toRun()
		}
	}
}

// Clone 深拷贝。
func (b *Bitmap) Clone() *Bitmap {
	nb := &Bitmap{
		keys:       append([]uint16(nil), b.keys...),
		containers: make([]*container, len(b.containers)),
	}
	for i, c := range b.containers {
		nb.containers[i] = c.clone()
	}
	return nb
}

// ===== 集合运算 =====

// Or 返回 a ∪ b(并集),不改动入参。
func Or(a, b *Bitmap) *Bitmap {
	r := a.Clone()
	r.Or(b)
	return r
}

// And 返回 a ∩ b(交集),不改动入参。
func And(a, b *Bitmap) *Bitmap {
	r := New()
	// 遍历较小者,减少 Contains 次数。
	small, large := a, b
	if small.Len() > large.Len() {
		small, large = large, small
	}
	small.Range(func(v uint32) bool {
		if large.Contains(v) {
			r.Add(v)
		}
		return true
	})
	return r
}

// AndNot 返回 a \ b(差集),不改动入参。
func AndNot(a, b *Bitmap) *Bitmap {
	r := New()
	a.Range(func(v uint32) bool {
		if !b.Contains(v) {
			r.Add(v)
		}
		return true
	})
	return r
}

// Or 就地并入 b。
func (b *Bitmap) Or(other *Bitmap) {
	other.Range(func(v uint32) bool {
		b.Add(v)
		return true
	})
}

// And 就地交 other(只保留同时在 other 中的值)。
func (b *Bitmap) And(other *Bitmap) {
	keep := And(b, other)
	b.keys, b.containers = keep.keys, keep.containers
}

// AndNot 就地减去 other。
func (b *Bitmap) AndNot(other *Bitmap) {
	other.Range(func(v uint32) bool {
		b.Remove(v)
		return true
	})
}
