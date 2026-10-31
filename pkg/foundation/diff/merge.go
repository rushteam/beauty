package diff

import "strings"

// 本文件提供「三路合并」(3-way merge):给定共同祖先 base 与两个分支 a、b,把两边各自
// 相对 base 的改动合并到一起。这是协同编辑、配置多人改动汇流、Git 式分支合并的核心。
//
// 规则(逐变更区间):
//   - 只有一边改了 base 的某段 → 取改动的那一边;
//   - 两边都没改 → 保留 base;
//   - 两边改成了相同内容 → 取该内容(非冲突);
//   - 两边改成了不同内容 → 冲突。冲突区间两边内容都记录在 Conflict 里,
//     合并结果里默认取 a 侧(ours),调用方据 Conflicts 自行裁决或渲染冲突标记。
//
// 算法:标准 diff3。分别对 (base,a)、(base,b) 用 LCS 求出各自相对 base 的变更 hunk,
// 再按 base 坐标把两侧 hunk 做区间重叠合并——不重叠的改动各取各的,重叠且不同才算冲突。
// 底层 LCS 为 O(n·m),适合配置/文档规模。泛型 Merge3[T comparable] 对任意可比较元素
// 工作;Merge3Lines 封装按行文本合并。

// Conflict 描述一处无法自动合并的冲突区间:base 原内容与 a、b 两侧各自的内容。
type Conflict[T comparable] struct {
	Base []T
	A    []T
	B    []T
}

// hunk 表示「把 base[oStart:oEnd) 替换为 repl」的一处变更(oEnd==oStart 即纯插入)。
type hunk[T comparable] struct {
	oStart, oEnd int
	repl         []T
}

// Merge3 把 a、b 相对共同祖先 base 的改动三路合并。返回合并结果与冲突列表;
// 冲突为空(len(conflicts)==0)表示干净合并。冲突区间在结果中默认取 a 侧。
func Merge3[T comparable](base, a, b []T) (merged []T, conflicts []Conflict[T]) {
	ca := diffHunks(base, a)
	cb := diffHunks(base, b)
	merged = make([]T, 0, max3(len(base), len(a), len(b)))

	ia, ib, oi := 0, 0, 0
	for {
		hasA, hasB := ia < len(ca), ib < len(cb)
		if !hasA && !hasB {
			merged = append(merged, base[oi:]...)
			break
		}
		// 下一处变更的起点。
		start := len(base)
		if hasA && ca[ia].oStart < start {
			start = ca[ia].oStart
		}
		if hasB && cb[ib].oStart < start {
			start = cb[ib].oStart
		}
		// 复制变更前的稳定段。
		if start > oi {
			merged = append(merged, base[oi:start]...)
		}
		// 把从 start 起相互重叠的 hunk 都并进同一区间。
		regionEnd := start
		var ah, bh []hunk[T]
		for {
			progressed := false
			for ia < len(ca) && (ca[ia].oStart < regionEnd || ca[ia].oStart == start) {
				h := ca[ia]
				ah = append(ah, h)
				if h.oEnd > regionEnd {
					regionEnd = h.oEnd
				}
				ia++
				progressed = true
			}
			for ib < len(cb) && (cb[ib].oStart < regionEnd || cb[ib].oStart == start) {
				h := cb[ib]
				bh = append(bh, h)
				if h.oEnd > regionEnd {
					regionEnd = h.oEnd
				}
				ib++
				progressed = true
			}
			if !progressed {
				break
			}
		}
		baseSeg := base[start:regionEnd]
		aSlice := reconstruct(base, ah, start, regionEnd)
		bSlice := reconstruct(base, bh, start, regionEnd)
		aEq, bEq := equalSeg(aSlice, baseSeg), equalSeg(bSlice, baseSeg)
		switch {
		case aEq && bEq:
			merged = append(merged, baseSeg...)
		case !aEq && bEq:
			merged = append(merged, aSlice...)
		case aEq && !bEq:
			merged = append(merged, bSlice...)
		default:
			if equalSeg(aSlice, bSlice) {
				merged = append(merged, aSlice...)
			} else {
				conflicts = append(conflicts, Conflict[T]{
					Base: cloneSeg(baseSeg), A: cloneSeg(aSlice), B: cloneSeg(bSlice),
				})
				merged = append(merged, aSlice...) // 默认取 ours
			}
		}
		oi = regionEnd
	}
	return merged, conflicts
}

// diffHunks 求出把 o 变成 s 的变更 hunk(相对 o 坐标),相邻的增删合并为一处。
func diffHunks[T comparable](o, s []T) []hunk[T] {
	var hunks []hunk[T]
	oi := 0
	var cur *hunk[T]
	flush := func() {
		if cur != nil {
			hunks = append(hunks, *cur)
			cur = nil
		}
	}
	for _, e := range Diff(o, s) {
		switch e.Kind {
		case OpEqual:
			flush()
			oi++
		case OpDelete:
			if cur == nil {
				cur = &hunk[T]{oStart: oi, oEnd: oi}
			}
			oi++
			cur.oEnd = oi
		case OpInsert:
			if cur == nil {
				cur = &hunk[T]{oStart: oi, oEnd: oi}
			}
			cur.repl = append(cur.repl, e.Elem)
		}
	}
	flush()
	return hunks
}

// reconstruct 用一侧的 hunk 在 base 窗口 [lo,hi) 上还原该侧内容。
func reconstruct[T comparable](o []T, hunks []hunk[T], lo, hi int) []T {
	out := make([]T, 0, hi-lo)
	p := lo
	for _, h := range hunks {
		if h.oStart > p {
			out = append(out, o[p:h.oStart]...)
		}
		out = append(out, h.repl...)
		p = h.oEnd
	}
	if p < hi {
		out = append(out, o[p:hi]...)
	}
	return out
}

func equalSeg[T comparable](a, b []T) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func cloneSeg[T comparable](s []T) []T {
	if len(s) == 0 {
		return nil
	}
	out := make([]T, len(s))
	copy(out, s)
	return out
}

func max3(a, b, c int) int {
	m := a
	if b > m {
		m = b
	}
	if c > m {
		m = c
	}
	return m
}

// ===== 文本(按行)便捷函数 =====

// Merge3Lines 对三段文本按行做三路合并。返回合并文本与冲突列表。
func Merge3Lines(base, a, b string) (string, []Conflict[string]) {
	merged, conflicts := Merge3(splitLines(base), splitLines(a), splitLines(b))
	return strings.Join(merged, "\n"), conflicts
}
