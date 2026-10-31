package roaring

import (
	"math/bits"
	"sort"
)

// containerType 标记容器的三种表示。
type containerType uint8

const (
	typeArray  containerType = iota // 稀疏:升序 []uint16
	typeBitmap                      // 稠密:1024×uint64 定长位图
	typeRun                         // 连续段:若干 [start, start+len) 游程(run-length)
)

// rle 是一段连续值的游程:覆盖 [Start, Start+Len)(Len>=1)。
type rle struct {
	start  uint16
	length uint16 // 连续值个数(>=1)
}

// container 是单个高 16 位桶内 65536 值域的集合,三种表示自适应:
//   - typeArray:arr 升序;
//   - typeBitmap:bitmap 定长位图,card 维护基数;
//   - typeRun:runs 游程表,card 维护基数(通常由 RunOptimize 产生,利于连续区间与序列化压缩)。
type container struct {
	typ    containerType
	arr    []uint16
	bitmap []uint64
	runs   []rle
	card   int
}

func (c *container) isArray() bool  { return c.typ == typeArray }
func (c *container) isBitmap() bool { return c.typ == typeBitmap }
func (c *container) isRun() bool    { return c.typ == typeRun }

func (c *container) cardinality() int {
	if c.typ == typeArray {
		return len(c.arr)
	}
	return c.card
}

// toMutable 把 run 容器物化回 array/bitmap,以便 add/remove 就地修改。
func (c *container) toMutable() {
	if c.typ != typeRun {
		return
	}
	if c.card > arrayThreshold {
		bm := make([]uint64, 1024)
		for _, r := range c.runs {
			for v := uint32(r.start); v < uint32(r.start)+uint32(r.length); v++ {
				bm[v>>6] |= 1 << (v & 63)
			}
		}
		c.bitmap, c.runs, c.typ = bm, nil, typeBitmap
		return
	}
	arr := make([]uint16, 0, c.card)
	for _, r := range c.runs {
		for i := uint32(0); i < uint32(r.length); i++ {
			arr = append(arr, uint16(uint32(r.start)+i))
		}
	}
	c.arr, c.runs, c.typ = arr, nil, typeArray
}

// searchArr 在数组里二分 v,返回下标与是否命中。
func (c *container) searchArr(v uint16) (int, bool) {
	i := sort.Search(len(c.arr), func(i int) bool { return c.arr[i] >= v })
	return i, i < len(c.arr) && c.arr[i] == v
}

func (c *container) contains(v uint16) bool {
	switch c.typ {
	case typeArray:
		_, ok := c.searchArr(v)
		return ok
	case typeBitmap:
		return c.bitmap[v>>6]&(1<<(v&63)) != 0
	default: // typeRun
		i := sort.Search(len(c.runs), func(i int) bool { return c.runs[i].start > v })
		// 候选游程是 i-1(其 start <= v)。
		if i == 0 {
			return false
		}
		r := c.runs[i-1]
		return v < r.start+r.length
	}
}

// add 加入 v,返回是否新增;数组超阈值时升级为位图。run 容器先物化为可变表示。
func (c *container) add(v uint16) bool {
	c.toMutable()
	if c.typ == typeArray {
		i, ok := c.searchArr(v)
		if ok {
			return false
		}
		c.arr = append(c.arr, 0)
		copy(c.arr[i+1:], c.arr[i:])
		c.arr[i] = v
		if len(c.arr) > arrayThreshold {
			c.toBitmap()
		}
		return true
	}
	w, bit := v>>6, uint64(1)<<(v&63)
	if c.bitmap[w]&bit != 0 {
		return false
	}
	c.bitmap[w] |= bit
	c.card++
	return true
}

func (c *container) remove(v uint16) bool {
	c.toMutable()
	if c.typ == typeArray {
		i, ok := c.searchArr(v)
		if !ok {
			return false
		}
		c.arr = append(c.arr[:i], c.arr[i+1:]...)
		return true
	}
	w, bit := v>>6, uint64(1)<<(v&63)
	if c.bitmap[w]&bit == 0 {
		return false
	}
	c.bitmap[w] &^= bit
	c.card--
	// 稠密回落到稀疏时降级为数组,省内存。
	if c.card <= arrayThreshold {
		c.toArray()
	}
	return true
}

// toBitmap 把数组容器升级为位图容器。
func (c *container) toBitmap() {
	c.bitmap = make([]uint64, 1024)
	for _, v := range c.arr {
		c.bitmap[v>>6] |= 1 << (v & 63)
	}
	c.card = len(c.arr)
	c.arr = nil
	c.typ = typeBitmap
}

// toArray 把位图容器降级为数组容器。
func (c *container) toArray() {
	arr := make([]uint16, 0, c.card)
	for w, word := range c.bitmap {
		for word != 0 {
			bit := word & -word
			idx := bits.TrailingZeros64(word)
			arr = append(arr, uint16(w<<6)|uint16(idx))
			word ^= bit
		}
	}
	c.arr = arr
	c.bitmap = nil
	c.typ = typeArray
}

// each 按升序遍历容器内所有低 16 位值。
func (c *container) each(fn func(v uint16) bool) {
	switch c.typ {
	case typeArray:
		for _, v := range c.arr {
			if !fn(v) {
				return
			}
		}
	case typeBitmap:
		for w, word := range c.bitmap {
			for word != 0 {
				bit := word & -word
				idx := bits.TrailingZeros64(word)
				if !fn(uint16(w<<6) | uint16(idx)) {
					return
				}
				word ^= bit
			}
		}
	default: // typeRun
		for _, r := range c.runs {
			for i := uint32(0); i < uint32(r.length); i++ {
				if !fn(uint16(uint32(r.start) + i)) {
					return
				}
			}
		}
	}
}

func (c *container) clone() *container {
	nc := &container{typ: c.typ, card: c.card}
	if c.arr != nil {
		nc.arr = append([]uint16(nil), c.arr...)
	}
	if c.bitmap != nil {
		nc.bitmap = append([]uint64(nil), c.bitmap...)
	}
	if c.runs != nil {
		nc.runs = append([]rle(nil), c.runs...)
	}
	return nc
}

// toRun 把当前容器压成游程表示(供 RunOptimize / 序列化使用)。
func (c *container) toRun() {
	if c.typ == typeRun {
		return
	}
	var runs []rle
	card := 0
	var cur rle
	have := false
	c.each(func(v uint16) bool {
		if have && uint32(cur.start)+uint32(cur.length) == uint32(v) {
			cur.length++
		} else {
			if have {
				runs = append(runs, cur)
			}
			cur = rle{start: v, length: 1}
			have = true
		}
		card++
		return true
	})
	if have {
		runs = append(runs, cur)
	}
	c.runs, c.arr, c.bitmap, c.card, c.typ = runs, nil, nil, card, typeRun
}

// runBytes 返回若压成游程表示大致占用的字节数(每游程 4 字节 + 2 字节计数)。
func (c *container) runBytes() int {
	// 需先知道游程数;在不改变状态下估算。
	n := 0
	var prev int = -2
	c.each(func(v uint16) bool {
		if int(v) != prev+1 {
			n++
		}
		prev = int(v)
		return true
	})
	return 2 + n*4
}

// arrayBytes / bitmapBytes 返回对应表示的字节数,用于 RunOptimize 选最省的。
func (c *container) arrayBytes() int  { return c.cardinality() * 2 }
func (c *container) bitmapBytes() int { return 1024 * 8 }
