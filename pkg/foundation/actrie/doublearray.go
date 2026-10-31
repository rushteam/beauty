package actrie

import "sort"

// DoubleArrayTrie 是「双数组 Trie(Double-Array Trie, DAT)」:把前缀树压成 base、check
// 两个整型数组,状态转移是一次加法 + 一次比较,O(1) 且极省内存——这是分词词典、静态路由、
// 只读大词典查找的经典结构。
//
// 与本包的 AC 自动机(Matcher/Dict)分工:
//   - AC 自动机:在长文本里「一次扫描同时匹配上万模式串」(含重叠),敏感词过滤首选,
//     支持构建后增量 Add + 归一化;
//   - DAT:静态只读词典的「精确匹配 / 最长(全部)前缀匹配」,内存极致紧凑、查询最快,
//     适合分词词典、路由表这类「构建一次、海量查询」的场景。
//
// 转移原理:状态 s 读入字节 b(编码 code=b+1,留 0 作保留)后,目标态 t = base[s]+code;
// 当且仅当 check[t]==s 时该转移存在。根为状态 1(下标 0 保留)。
//
// 按 **字节** 组织(非 rune):UTF-8 里 NUL 不出现于合法多字节序列,中文按字节精确匹配
// 不会误配。构建后只读,可多 goroutine 并发查询;构建期非并发安全。零值不可用,用
// BuildDoubleArray / BuildDoubleArraySet 构造。
type DoubleArrayTrie[V any] struct {
	base   []int
	check  []int
	hasVal []bool
	vals   []V
	size   int // 词条数
}

// 构建期用的显式 trie 节点。
type datNode[V any] struct {
	children map[byte]*datNode[V]
	terminal bool
	val      V
}

// BuildDoubleArray 从 key→value 词典构建 DAT。重复 key 后者覆盖;空 key 记到根上。
func BuildDoubleArray[V any](entries map[string]V) *DoubleArrayTrie[V] {
	root := &datNode[V]{children: map[byte]*datNode[V]{}}
	size := 0
	for k, v := range entries {
		n := root
		for i := 0; i < len(k); i++ {
			c := k[i]
			child, ok := n.children[c]
			if !ok {
				child = &datNode[V]{children: map[byte]*datNode[V]{}}
				n.children[c] = child
			}
			n = child
		}
		if !n.terminal {
			size++
		}
		n.terminal = true
		n.val = v
	}
	t := &DoubleArrayTrie[V]{size: size}
	t.pack(root)
	return t
}

// BuildDoubleArraySet 从字符串集合构建 DAT(只判存,无 payload)。
func BuildDoubleArraySet(keys []string) *DoubleArrayTrie[struct{}] {
	m := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		m[k] = struct{}{}
	}
	return BuildDoubleArray(m)
}

// pack 把显式 trie 按 BFS 打包进 base/check 双数组。
func (t *DoubleArrayTrie[V]) pack(root *datNode[V]) {
	t.ensure(1)
	// 根 = 状态 1。
	type item struct {
		node *datNode[V]
		s    int
	}
	if root.terminal {
		t.hasVal[1] = true
		t.vals[1] = root.val
	}
	queue := []item{{root, 1}}
	minFree := 2 // 下一个可能空闲的下标(单调前移,避免每次从头找 base)
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		n := it.node
		if len(n.children) == 0 {
			continue
		}
		// 子节点字节升序 → code 升序。
		bytesSorted := make([]int, 0, len(n.children))
		for c := range n.children {
			bytesSorted = append(bytesSorted, int(c))
		}
		sort.Ints(bytesSorted)
		codes := make([]int, len(bytesSorted))
		for i, b := range bytesSorted {
			codes[i] = b + 1
		}
		base := t.findBase(codes, &minFree)
		t.base[it.s] = base
		for i, b := range bytesSorted {
			child := n.children[byte(b)]
			ci := base + codes[i]
			t.ensure(ci)
			t.check[ci] = it.s
			if child.terminal {
				t.hasVal[ci] = true
				t.vals[ci] = child.val
			}
			queue = append(queue, item{child, ci})
		}
	}
}

// findBase 为一组 code 找一个 base,使每个 base+code 槽都空闲(check==0)。
func (t *DoubleArrayTrie[V]) findBase(codes []int, minFree *int) int {
	base := *minFree - codes[0]
	if base < 1 {
		base = 1
	}
	for {
		last := base + codes[len(codes)-1]
		t.ensure(last)
		ok := true
		for _, c := range codes {
			if t.check[base+c] != 0 {
				ok = false
				break
			}
		}
		if ok {
			// 前移 minFree 到首个仍空闲的下标。
			for *minFree < len(t.check) && t.check[*minFree] != 0 {
				*minFree++
			}
			return base
		}
		base++
	}
}

// ensure 保证数组可索引到下标 idx。
func (t *DoubleArrayTrie[V]) ensure(idx int) {
	if idx < len(t.check) {
		return
	}
	n := len(t.check)
	if n == 0 {
		n = 2
	}
	for n <= idx {
		n *= 2
	}
	base := make([]int, n)
	check := make([]int, n)
	hasVal := make([]bool, n)
	vals := make([]V, n)
	copy(base, t.base)
	copy(check, t.check)
	copy(hasVal, t.hasVal)
	copy(vals, t.vals)
	t.base, t.check, t.hasVal, t.vals = base, check, hasVal, vals
}

// walk 从状态 s 读字节 b,返回目标态与是否存在转移。
func (t *DoubleArrayTrie[V]) walk(s int, b byte) (int, bool) {
	tgt := t.base[s] + int(b) + 1
	if tgt < len(t.check) && t.check[tgt] == s {
		return tgt, true
	}
	return 0, false
}

// Get 精确查找 key,命中返回其 value 与 true。
func (t *DoubleArrayTrie[V]) Get(key string) (V, bool) {
	s := 1
	for i := 0; i < len(key); i++ {
		next, ok := t.walk(s, key[i])
		if !ok {
			var zero V
			return zero, false
		}
		s = next
	}
	if t.hasVal[s] {
		return t.vals[s], true
	}
	var zero V
	return zero, false
}

// Contains 判断 key 是否为词典中的一个完整词条。
func (t *DoubleArrayTrie[V]) Contains(key string) bool {
	_, ok := t.Get(key)
	return ok
}

// PrefixHit 是一次「前缀命中」:text 的前 ByteLen 个字节构成一个词典词条,携带其 Value。
type PrefixHit[V any] struct {
	ByteLen int
	Value   V
}

// CommonPrefixSearch 返回 text 的所有「前缀词条」(按长度升序):凡是 text 某个前缀恰为
// 词典中一个词的,都回报其字节长度与 value。这是词典分词(正向最大/全切分)的核心原语。
func (t *DoubleArrayTrie[V]) CommonPrefixSearch(text string) []PrefixHit[V] {
	var hits []PrefixHit[V]
	s := 1
	for i := 0; i < len(text); i++ {
		next, ok := t.walk(s, text[i])
		if !ok {
			break
		}
		s = next
		if t.hasVal[s] {
			hits = append(hits, PrefixHit[V]{ByteLen: i + 1, Value: t.vals[s]})
		}
	}
	return hits
}

// LongestPrefix 返回 text 的最长前缀词条(最大匹配),没有则 ok=false。
func (t *DoubleArrayTrie[V]) LongestPrefix(text string) (prefix string, val V, ok bool) {
	s := 1
	best := -1
	var bestVal V
	for i := 0; i < len(text); i++ {
		next, w := t.walk(s, text[i])
		if !w {
			break
		}
		s = next
		if t.hasVal[s] {
			best = i + 1
			bestVal = t.vals[s]
		}
	}
	if best < 0 {
		var zero V
		return "", zero, false
	}
	return text[:best], bestVal, true
}

// Size 返回词条数。
func (t *DoubleArrayTrie[V]) Size() int { return t.size }
