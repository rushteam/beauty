package diff

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// 本文件在 LCS 行/序列 diff 之外,补上「结构化 JSON diff」与 RFC 6902 JSON Patch 应用。
//
// 适用场景:
//   - 配置中心按「字段」而非「整行」下发:只推变化的 key,审计噪声最小;
//   - API 局部更新(PATCH 语义)、文档协同的结构化合并;
//   - 幂等回放:把补丁应用到已知基线,带 test 操作做乐观并发校验。
//
// 文档模型用 encoding/json 解码后的通用类型:map[string]any(对象)、[]any(数组)、
// float64 / string / bool / nil(标量)。路径用 RFC 6901 JSON Pointer("/a/b/0")。
//
// JSONDiff 产出 add/remove/replace 三类操作(不生成 move/copy,保持最小且确定);
// ApplyJSON 支持完整 RFC 6902 六种操作 add/remove/replace/move/copy/test。
// 纯标准库、无状态、并发安全。

// JSONOp 是一条 RFC 6902 操作。
type JSONOp struct {
	Op    string `json:"op"`              // add / remove / replace / move / copy / test
	Path  string `json:"path"`            // 目标 JSON Pointer
	From  string `json:"from,omitempty"`  // move / copy 的源 JSON Pointer
	Value any    `json:"value,omitempty"` // add / replace / test 的值
}

// JSONDiff 计算把 a 变成 b 的结构化补丁(add/remove/replace)。a、b 为 JSON 解码后的
// 通用值。对象按 key 递归;数组按下标对齐递归,长度不一致时整体 replace(保持简单确定,
// 不做数组内 LCS,避免生成含歧义的 move);标量/类型不同则 replace。
func JSONDiff(a, b any) []JSONOp {
	var ops []JSONOp
	jsonDiff("", a, b, &ops)
	return ops
}

func jsonDiff(path string, a, b any, ops *[]JSONOp) {
	if reflect.DeepEqual(a, b) {
		return
	}
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if aok && bok {
		// 删除 a 有而 b 无的 key(先删,避免与新增混淆)。
		for k := range am {
			if _, ok := bm[k]; !ok {
				*ops = append(*ops, JSONOp{Op: "remove", Path: path + "/" + escapeToken(k)})
			}
		}
		for k, bv := range bm {
			p := path + "/" + escapeToken(k)
			if av, ok := am[k]; ok {
				jsonDiff(p, av, bv, ops)
			} else {
				*ops = append(*ops, JSONOp{Op: "add", Path: p, Value: bv})
			}
		}
		return
	}
	as, aok := a.([]any)
	bs, bok := b.([]any)
	if aok && bok {
		if len(as) == len(bs) {
			// 等长:按下标对齐,逐元素递归(可生成元素内部的最小字段级补丁)。
			for i := range bs {
				jsonDiff(path+"/"+strconv.Itoa(i), as[i], bs[i], ops)
			}
		} else {
			// 长度不同:按元素做 LCS 对齐,生成 add/remove(而非整体 replace),最小化变更。
			jsonArrayDiff(path, as, bs, ops)
		}
		return
	}
	// 标量、类型不同:整体替换。根路径用 replace 覆盖整个文档。
	*ops = append(*ops, JSONOp{Op: "replace", Path: path, Value: b})
}

// jsonArrayDiff 对两个数组按元素(reflect.DeepEqual)做 LCS 对齐,生成随索引移动的
// add/remove 操作:相等元素跳过,a 多出的删除,b 多出的插入。按产出顺序应用即可把 a 变成 b。
func jsonArrayDiff(path string, as, bs []any, ops *[]JSONOp) {
	kinds := lcsAnyEdits(as, bs)
	idx := 0
	ai, bi := 0, 0
	for _, k := range kinds {
		switch k {
		case OpEqual:
			ai++
			bi++
			idx++
		case OpDelete:
			*ops = append(*ops, JSONOp{Op: "remove", Path: path + "/" + strconv.Itoa(idx)})
			ai++ // 删除后索引不前移(元素左移)
		case OpInsert:
			*ops = append(*ops, JSONOp{Op: "add", Path: path + "/" + strconv.Itoa(idx), Value: bs[bi]})
			bi++
			idx++
		}
	}
}

// lcsAnyEdits 对 []any 按 reflect.DeepEqual 求 LCS 编辑脚本(只返回操作类型序列)。
func lcsAnyEdits(a, b []any) []OpKind {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if reflect.DeepEqual(a[i], b[j]) {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	out := make([]OpKind, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case reflect.DeepEqual(a[i], b[j]):
			out = append(out, OpEqual)
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			out = append(out, OpDelete)
			i++
		default:
			out = append(out, OpInsert)
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, OpDelete)
	}
	for ; j < m; j++ {
		out = append(out, OpInsert)
	}
	return out
}

// ApplyJSON 把 RFC 6902 补丁应用到 doc,返回新文档;任一操作失败则整体返回错误
// (RFC 要求原子性,这里在副本上操作,失败不影响入参)。doc 不会被修改。
func ApplyJSON(doc any, patch []JSONOp) (any, error) {
	cur := deepCopy(doc)
	for i, op := range patch {
		var err error
		switch op.Op {
		case "add":
			cur, err = jsonAdd(cur, op.Path, deepCopy(op.Value))
		case "remove":
			cur, _, err = jsonRemove(cur, op.Path)
		case "replace":
			if _, err = jsonGet(cur, op.Path); err == nil {
				cur, _, err = jsonRemove(cur, op.Path)
				if err == nil {
					cur, err = jsonAdd(cur, op.Path, deepCopy(op.Value))
				}
			}
		case "test":
			var got any
			if got, err = jsonGet(cur, op.Path); err == nil && !reflect.DeepEqual(got, op.Value) {
				err = fmt.Errorf("test failed: %v != %v", got, op.Value)
			}
		case "move":
			var v any
			if cur, v, err = jsonRemove(cur, op.From); err == nil {
				cur, err = jsonAdd(cur, op.Path, v)
			}
		case "copy":
			var v any
			if v, err = jsonGet(cur, op.From); err == nil {
				cur, err = jsonAdd(cur, op.Path, deepCopy(v))
			}
		default:
			err = fmt.Errorf("unknown op %q", op.Op)
		}
		if err != nil {
			return nil, fmt.Errorf("diff: json patch op %d (%s %s): %w", i, op.Op, op.Path, err)
		}
	}
	return cur, nil
}

// ===== JSON Pointer(RFC 6901)与文档导航 =====

// parsePointer 把 JSON Pointer 拆成 token 序列。空串表示根(返回 nil)。
func parsePointer(p string) ([]string, error) {
	if p == "" {
		return nil, nil
	}
	if p[0] != '/' {
		return nil, fmt.Errorf("invalid pointer %q: must start with /", p)
	}
	parts := strings.Split(p[1:], "/")
	for i, t := range parts {
		parts[i] = unescapeToken(t)
	}
	return parts, nil
}

// escapeToken 转义 key 中的 ~ 与 /(RFC 6901:~ → ~0,/ → ~1)。
func escapeToken(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	return strings.ReplaceAll(s, "/", "~1")
}

func unescapeToken(s string) string {
	s = strings.ReplaceAll(s, "~1", "/")
	return strings.ReplaceAll(s, "~0", "~")
}

// jsonGet 取 path 处的值。
func jsonGet(doc any, path string) (any, error) {
	tokens, err := parsePointer(path)
	if err != nil {
		return nil, err
	}
	cur := doc
	for _, t := range tokens {
		switch c := cur.(type) {
		case map[string]any:
			v, ok := c[t]
			if !ok {
				return nil, fmt.Errorf("key %q not found", t)
			}
			cur = v
		case []any:
			idx, err := arrayIndex(t, len(c), false)
			if err != nil {
				return nil, err
			}
			cur = c[idx]
		default:
			return nil, fmt.Errorf("cannot descend into %T at %q", cur, t)
		}
	}
	return cur, nil
}

// jsonAdd 在 path 处新增/设置值,返回新文档。空 path 表示替换整个文档。
func jsonAdd(doc any, path string, value any) (any, error) {
	tokens, err := parsePointer(path)
	if err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return value, nil
	}
	return addRec(doc, tokens, value)
}

func addRec(node any, tokens []string, value any) (any, error) {
	t := tokens[0]
	last := len(tokens) == 1
	switch c := node.(type) {
	case map[string]any:
		if last {
			c[t] = value
			return c, nil
		}
		child, ok := c[t]
		if !ok {
			return nil, fmt.Errorf("key %q not found", t)
		}
		nc, err := addRec(child, tokens[1:], value)
		if err != nil {
			return nil, err
		}
		c[t] = nc
		return c, nil
	case []any:
		if last {
			if t == "-" {
				return append(c, value), nil
			}
			idx, err := arrayIndex(t, len(c), true)
			if err != nil {
				return nil, err
			}
			out := make([]any, 0, len(c)+1)
			out = append(out, c[:idx]...)
			out = append(out, value)
			out = append(out, c[idx:]...)
			return out, nil
		}
		idx, err := arrayIndex(t, len(c), false)
		if err != nil {
			return nil, err
		}
		nc, err := addRec(c[idx], tokens[1:], value)
		if err != nil {
			return nil, err
		}
		c[idx] = nc
		return c, nil
	default:
		return nil, fmt.Errorf("cannot add into %T at %q", node, t)
	}
}

// jsonRemove 删除 path 处的值,返回新文档与被删值。
func jsonRemove(doc any, path string) (any, any, error) {
	tokens, err := parsePointer(path)
	if err != nil {
		return nil, nil, err
	}
	if len(tokens) == 0 {
		return nil, doc, nil // 删除根:返回 nil 文档
	}
	return removeRec(doc, tokens)
}

func removeRec(node any, tokens []string) (any, any, error) {
	t := tokens[0]
	last := len(tokens) == 1
	switch c := node.(type) {
	case map[string]any:
		child, ok := c[t]
		if !ok {
			return nil, nil, fmt.Errorf("key %q not found", t)
		}
		if last {
			delete(c, t)
			return c, child, nil
		}
		nc, removed, err := removeRec(child, tokens[1:])
		if err != nil {
			return nil, nil, err
		}
		c[t] = nc
		return c, removed, nil
	case []any:
		idx, err := arrayIndex(t, len(c), false)
		if err != nil {
			return nil, nil, err
		}
		if last {
			removed := c[idx]
			out := make([]any, 0, len(c)-1)
			out = append(out, c[:idx]...)
			out = append(out, c[idx+1:]...)
			return out, removed, nil
		}
		nc, removed, err := removeRec(c[idx], tokens[1:])
		if err != nil {
			return nil, nil, err
		}
		c[idx] = nc
		return c, removed, nil
	default:
		return nil, nil, fmt.Errorf("cannot remove from %T at %q", node, t)
	}
}

// arrayIndex 解析数组下标。allowEnd 为 true 时允许等于 len(用于 add 的尾插位置)。
func arrayIndex(t string, n int, allowEnd bool) (int, error) {
	idx, err := strconv.Atoi(t)
	if err != nil {
		return 0, fmt.Errorf("invalid array index %q", t)
	}
	hi := n
	if !allowEnd {
		hi = n - 1
	}
	if idx < 0 || idx > hi {
		return 0, fmt.Errorf("array index %d out of range", idx)
	}
	return idx, nil
}

// deepCopy 深拷贝 JSON 通用值,避免补丁操作改到入参(或在 value 间共享引用)。
func deepCopy(v any) any {
	switch c := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(c))
		for k, val := range c {
			out[k] = deepCopy(val)
		}
		return out
	case []any:
		out := make([]any, len(c))
		for i, val := range c {
			out[i] = deepCopy(val)
		}
		return out
	default:
		return v
	}
}
