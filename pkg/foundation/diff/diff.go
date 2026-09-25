// Package diff 提供基于最长公共子序列(LCS)的差异计算与补丁应用:算出把序列 a 变成
// 序列 b 的最小编辑脚本(保留 / 删除 / 插入),并能把该脚本(Patch)应用回 a 得到 b。
//
// 适用场景:
//   - 配置中心动态下发:只算出"变了哪些行/键",最小化推送与审计噪声;
//   - 操作日志 / 审计:记录"谁把哪几行改成了什么";
//   - 协同编辑 / 版本对比:文本按行 diff,结构化数据按元素 diff;
//   - 幂等回放:把补丁应用到已知基线,校验结果一致。
//
// 泛型 Diff[T comparable] 对任意可比较元素(行、token、ID、字符 rune)工作;文本便捷函数
// DiffLines / Format / ApplyLines 封装了"按行"这一最常见用法。
//
// 算法:标准 LCS 动态规划(O(n·m) 时间与空间),回溯生成编辑脚本。适合配置/文档这类
// 规模(几千行内)。若要处理超大输入或追求最短编辑距离,可换 Myers O(ND) 算法(本包
// 未实现,保持零依赖与实现简单)。
//
// 无状态、纯函数,天然并发安全。
package diff

import "strings"

// OpKind 编辑操作类型。
type OpKind int

const (
	// OpEqual 该元素在 a、b 中相同(保留)。
	OpEqual OpKind = iota
	// OpInsert 在 b 中新增的元素(a 中没有)。
	OpInsert
	// OpDelete 从 a 中删除的元素(b 中没有)。
	OpDelete
)

// String 返回操作的符号:" "(保留)、"+"(插入)、"-"(删除)。
func (k OpKind) String() string {
	switch k {
	case OpEqual:
		return " "
	case OpInsert:
		return "+"
	case OpDelete:
		return "-"
	default:
		return "?"
	}
}

// Edit 一条编辑:操作类型 + 涉及的元素。
type Edit[T comparable] struct {
	Kind OpKind
	Elem T
}

// Patch 是一组有序编辑,描述如何把 a 变成 b。
type Patch[T comparable] []Edit[T]

// Diff 计算把 a 变成 b 的编辑脚本(LCS)。相同前后缀会被标为 OpEqual,
// 差异部分表现为一段 OpDelete(来自 a)与 OpInsert(来自 b)。
func Diff[T comparable](a, b []T) Patch[T] {
	n, m := len(a), len(b)
	// dp[i][j] = a[i:] 与 b[j:] 的 LCS 长度
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	// 回溯生成编辑脚本
	edits := make(Patch[T], 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			edits = append(edits, Edit[T]{Kind: OpEqual, Elem: a[i]})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			edits = append(edits, Edit[T]{Kind: OpDelete, Elem: a[i]})
			i++
		default:
			edits = append(edits, Edit[T]{Kind: OpInsert, Elem: b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		edits = append(edits, Edit[T]{Kind: OpDelete, Elem: a[i]})
	}
	for ; j < m; j++ {
		edits = append(edits, Edit[T]{Kind: OpInsert, Elem: b[j]})
	}
	return edits
}

// Apply 把补丁应用到 a,返回变换后的序列(即 b)。
// 若补丁与 a 不匹配(OpEqual/OpDelete 处元素对不上,或 a 有剩余未消费元素)返回错误,
// 用于检测"补丁基线不一致"(如配置在生成补丁后又被别人改过)。
func Apply[T comparable](a []T, patch Patch[T]) ([]T, error) {
	out := make([]T, 0, len(a))
	i := 0
	for idx, e := range patch {
		switch e.Kind {
		case OpEqual:
			if i >= len(a) || a[i] != e.Elem {
				return nil, &MismatchError{Index: idx, Pos: i}
			}
			out = append(out, a[i])
			i++
		case OpDelete:
			if i >= len(a) || a[i] != e.Elem {
				return nil, &MismatchError{Index: idx, Pos: i}
			}
			i++
		case OpInsert:
			out = append(out, e.Elem)
		}
	}
	if i != len(a) {
		return nil, &MismatchError{Index: len(patch), Pos: i}
	}
	return out, nil
}

// MismatchError 表示补丁与源序列不一致(基线漂移)。
type MismatchError struct {
	Index int // 出错的编辑下标
	Pos   int // 源序列中的位置
}

func (e *MismatchError) Error() string {
	return "diff: patch does not apply cleanly (edit " + itoa(e.Index) + ", src pos " + itoa(e.Pos) + ")"
}

// Stats 统计补丁中保留/插入/删除的元素数。
func (p Patch[T]) Stats() (equal, insert, delete int) {
	for _, e := range p {
		switch e.Kind {
		case OpEqual:
			equal++
		case OpInsert:
			insert++
		case OpDelete:
			delete++
		}
	}
	return
}

// ===== 文本(按行)便捷函数 =====

// splitLines 按 \n 切行(去掉行尾统一,不保留换行符)。
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// DiffLines 对两段文本按行做 diff。
func DiffLines(a, b string) Patch[string] {
	return Diff(splitLines(a), splitLines(b))
}

// ApplyLines 把行补丁应用到文本 a,返回新文本。
func ApplyLines(a string, patch Patch[string]) (string, error) {
	out, err := Apply(splitLines(a), patch)
	if err != nil {
		return "", err
	}
	return strings.Join(out, "\n"), nil
}

// Format 把补丁渲染成人类可读的差异文本:每行以 " "(未变)、"+"(新增)、"-"(删除)开头。
// 适合日志/审计展示。
func Format[T comparable](patch Patch[T]) string {
	var b strings.Builder
	for _, e := range patch {
		b.WriteString(e.Kind.String())
		b.WriteString(sprint(e.Elem))
		b.WriteByte('\n')
	}
	return b.String()
}

// FormatCompact 只渲染变更行(跳过 OpEqual),差异大时更清爽。
func FormatCompact[T comparable](patch Patch[T]) string {
	var b strings.Builder
	for _, e := range patch {
		if e.Kind == OpEqual {
			continue
		}
		b.WriteString(e.Kind.String())
		b.WriteString(sprint(e.Elem))
		b.WriteByte('\n')
	}
	return b.String()
}
