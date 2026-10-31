package diff

// DiffMyers 用 Myers 的 O(ND) 差分算法计算把 a 变成 b 的最小编辑脚本(D 为编辑距离)。
//
// 与本包的 Diff(LCS 动态规划,O(n·m) 时间与空间)相比:当两序列差异小(D 小)时,Myers
// 快得多、且内存随 D 线性,适合大文件/长序列的近似相同比对(配置整文件、代码行 diff)。
// 两者产出同构的 Patch(OpEqual/OpInsert/OpDelete),可被同一个 Apply 应用。差异大时二者
// 复杂度接近,按需选用。
//
// 算法:在编辑图上做贪心的「D-路径」扩展,记录每一步的前沿 V,到达终点后回溯出编辑脚本。
func DiffMyers[T comparable](a, b []T) Patch[T] {
	n, m := len(a), len(b)
	if n == 0 && m == 0 {
		return nil
	}
	maxD := n + m
	off := maxD // 把 k∈[-maxD,maxD] 映射到 [0,2maxD] 的偏移
	v := make([]int, 2*maxD+1)
	trace := make([][]int, 0, maxD+1)

	var reached bool
	for d := 0; d <= maxD; d++ {
		trace = append(trace, append([]int(nil), v...)) // 保存本轮前的前沿,供回溯
		for k := -d; k <= d; k += 2 {
			var x int
			// 选择从上((k+1))还是从左((k-1))延展:取 x 更大的一侧。
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1] // 向下:插入 b 的一个元素
			} else {
				x = v[off+k-1] + 1 // 向右:删除 a 的一个元素
			}
			y := x - k
			// 沿对角线(相等元素)尽量前进。
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[off+k] = x
			if x >= n && y >= m {
				reached = true
				break
			}
		}
		if reached {
			break
		}
	}

	// 从 (n,m) 回溯编辑脚本(先逆序收集,再反转)。
	edits := make(Patch[T], 0, n+m)
	x, y := n, m
	for d := len(trace) - 1; d > 0; d-- {
		vv := trace[d]
		k := x - y
		var prevK int
		if k == -d || (k != d && vv[off+k-1] < vv[off+k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := vv[off+prevK]
		prevY := prevX - prevK
		// 回退对角线上的相等元素。
		for x > prevX && y > prevY {
			edits = append(edits, Edit[T]{Kind: OpEqual, Elem: a[x-1]})
			x--
			y--
		}
		if x == prevX {
			edits = append(edits, Edit[T]{Kind: OpInsert, Elem: b[y-1]})
		} else {
			edits = append(edits, Edit[T]{Kind: OpDelete, Elem: a[x-1]})
		}
		x, y = prevX, prevY
	}
	// d==0:回退起点处剩余的相等元素。
	for x > 0 && y > 0 {
		edits = append(edits, Edit[T]{Kind: OpEqual, Elem: a[x-1]})
		x--
		y--
	}

	// 反转成正序。
	for i, j := 0, len(edits)-1; i < j; i, j = i+1, j-1 {
		edits[i], edits[j] = edits[j], edits[i]
	}
	return edits
}

// DiffMyersLines 对两段文本按行做 Myers diff。
func DiffMyersLines(a, b string) Patch[string] {
	return DiffMyers(splitLines(a), splitLines(b))
}
