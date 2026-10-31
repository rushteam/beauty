package actrie

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// DoubleArrayAC 是「双数组压缩的 Aho-Corasick 自动机」:把 AC 的 goto(trie 转移)表压进
// base/check 两个整型数组,再配一个 fail 数组存失配指针——于是「多模匹配」既有 AC 的
// O(文本长度 + 命中数) 时间,又有双数组的极致省内存。适合**只读的大词典**(百万级敏感词/
// 实体词典)在内存里常驻 + 高频扫描。
//
// 与 Matcher/Dict(基于 map 的 goto)的取舍:Matcher 支持构建后增量 Add、归一化、payload、
// 命中位置按 rune 精确映射,功能全、改词方便;DoubleArrayAC 是**静态只读 + 字节级**,牺牲
// 灵活性换内存与扫描局部性。二者 API 风格一致(FindAll/Contains),按场景选。
//
// 按字节构建与匹配,命中位置为**字节**下标 [Start, End)。构建后只读,可并发查询。零值不可用,
// 用 BuildDoubleArrayAC 构造。
type DoubleArrayAC struct {
	base      []int
	check     []int
	fail      []int
	patLen    []int           // 每个状态作为终结态对应的(归一化后)模式串字节长度(0=非终结)
	patID     []int           // 终结态对应的模式串下标(-1=非终结)
	patterns  []string        // 去重后的模式串(原文,用于命中回报)
	normalize func(rune) rune // rune 级归一化;nil 表示按字节原样匹配(零分配快路径)
}

// acNode 构建期的显式 trie 节点。
type acNode struct {
	children map[byte]*acNode
	index    int // 打包后分配的双数组状态下标
	patLen   int
	patID    int
}

// BuildDoubleArrayAC 从一组模式串构建双数组 AC 自动机。空串忽略,重复只保留一份。
func BuildDoubleArrayAC(patterns ...string) *DoubleArrayAC {
	return BuildDoubleArrayACWith(patterns, nil)
}

// BuildDoubleArrayACWith 带选项构建双数组 AC 自动机,复用 WithNormalizer 做大小写折叠、
// 视觉混淆映射、剥离干扰符等「反绕过」归一化。归一化在构建(模式串)与查询(文本)两端一致生效;
// 命中仍回报**原文模式串**,位置按**原文字节**下标映射回去([Start, End) 指向原始 text)。
// 空串与归一化后为空的模式串忽略,原文重复只保留一份。
func BuildDoubleArrayACWith(patterns []string, opts []Option) *DoubleArrayAC {
	var cfg config
	for _, opt := range opts {
		opt(&cfg)
	}
	root := &acNode{children: map[byte]*acNode{}, patID: -1}
	m := &DoubleArrayAC{normalize: cfg.normalize}
	seen := map[string]int{}
	for _, p := range patterns {
		if p == "" {
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		np := p
		if cfg.normalize != nil {
			np = normalizeString(p, cfg.normalize)
		}
		if np == "" {
			continue
		}
		id := len(m.patterns)
		seen[p] = id
		m.patterns = append(m.patterns, p) // 存原文,命中按原文回报
		n := root
		for i := 0; i < len(np); i++ {
			c := np[i]
			child, ok := n.children[c]
			if !ok {
				child = &acNode{children: map[byte]*acNode{}, patID: -1}
				n.children[c] = child
			}
			n = child
		}
		n.patLen = len(np) // 归一化后的字节长度(用于在归一化流中回推起点)
		n.patID = id
	}
	m.pack(root)
	m.buildFail(root)
	return m
}

// normalizeString 对字符串逐 rune 归一化;fn 返回 0 表示跳过该字符。
func normalizeString(s string, fn func(rune) rune) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if nr := fn(r); nr != 0 {
			b.WriteRune(nr)
		}
	}
	return b.String()
}

// pack 把显式 trie 按 BFS 打包进 base/check 双数组,并记录每个节点分配到的状态下标。
func (m *DoubleArrayAC) pack(root *acNode) {
	m.ensure(1)
	root.index = 1
	m.setPattern(1, root)
	queue := []*acNode{root}
	minFree := 2
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if len(n.children) == 0 {
			continue
		}
		bytesSorted := make([]int, 0, len(n.children))
		for c := range n.children {
			bytesSorted = append(bytesSorted, int(c))
		}
		sort.Ints(bytesSorted)
		codes := make([]int, len(bytesSorted))
		for i, b := range bytesSorted {
			codes[i] = b + 1
		}
		base := m.findBase(codes, &minFree)
		m.base[n.index] = base
		for i, b := range bytesSorted {
			child := n.children[byte(b)]
			ci := base + codes[i]
			m.ensure(ci)
			m.check[ci] = n.index
			child.index = ci
			m.setPattern(ci, child)
			queue = append(queue, child)
		}
	}
}

func (m *DoubleArrayAC) setPattern(idx int, n *acNode) {
	m.patLen[idx] = n.patLen
	m.patID[idx] = n.patID
}

// buildFail 用 BFS 计算每个状态的失配指针(fail),并用双数组 goto 做转移。
func (m *DoubleArrayAC) buildFail(root *acNode) {
	m.fail = make([]int, len(m.check))
	m.fail[1] = 1
	queue := []*acNode{root}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for b, child := range p.children {
			ci := child.index
			if p.index == 1 {
				m.fail[ci] = 1
			} else {
				f := m.fail[p.index]
				for {
					if next, ok := m.gotoState(f, b); ok {
						m.fail[ci] = next
						break
					}
					if f == 1 {
						m.fail[ci] = 1
						break
					}
					f = m.fail[f]
				}
			}
			queue = append(queue, child)
		}
	}
}

// gotoState 从状态 s 读字节 b,返回目标态与是否存在 trie 转移(不含 fail)。
func (m *DoubleArrayAC) gotoState(s int, b byte) (int, bool) {
	t := m.base[s] + int(b) + 1
	if t < len(m.check) && m.check[t] == s {
		return t, true
	}
	return 0, false
}

// findBase 为一组 code 找一个 base,使每个 base+code 槽都空闲(check==0)。
func (m *DoubleArrayAC) findBase(codes []int, minFree *int) int {
	base := *minFree - codes[0]
	if base < 1 {
		base = 1
	}
	for {
		m.ensure(base + codes[len(codes)-1])
		ok := true
		for _, c := range codes {
			if m.check[base+c] != 0 {
				ok = false
				break
			}
		}
		if ok {
			for *minFree < len(m.check) && m.check[*minFree] != 0 {
				*minFree++
			}
			return base
		}
		base++
	}
}

// ensure 保证各数组可索引到下标 idx。
func (m *DoubleArrayAC) ensure(idx int) {
	if idx < len(m.check) {
		return
	}
	n := len(m.check)
	if n == 0 {
		n = 2
	}
	for n <= idx {
		n *= 2
	}
	base := make([]int, n)
	check := make([]int, n)
	patLen := make([]int, n)
	patID := make([]int, n)
	copy(base, m.base)
	copy(check, m.check)
	copy(patLen, m.patLen)
	copy(patID, m.patID)
	m.base, m.check, m.patLen, m.patID = base, check, patLen, patID
}

// next 在匹配中从状态 s 读字节 b:无直接转移则沿 fail 链回退,直至有转移或回到根。
func (m *DoubleArrayAC) next(s int, b byte) int {
	for {
		if t, ok := m.gotoState(s, b); ok {
			return t
		}
		if s == 1 {
			return 1 // 停在根
		}
		s = m.fail[s]
	}
}

// FindAll 返回文本中全部命中(含重叠),按结束位置升序。位置为**原文字节**下标 [Start, End)。
// 未设归一化时走零分配字节快路径;设了归一化时先把文本归一化并记录到原文的位置映射。
func (m *DoubleArrayAC) FindAll(text string) []Match {
	if m.normalize == nil {
		return m.findAllRaw(text)
	}
	nb, startOf, endOf := m.normalizeText(text)
	var out []Match
	s := 1
	for i := 0; i < len(nb); i++ {
		s = m.next(s, nb[i])
		for t := s; t != 1; t = m.fail[t] {
			if L := m.patLen[t]; L > 0 {
				out = append(out, Match{
					Pattern: m.patterns[m.patID[t]],
					Start:   startOf[i-L+1], // 归一化起点字节 → 原文字节
					End:     endOf[i],       // 归一化末字节 → 其源 rune 的原文结束
				})
			}
		}
	}
	return out
}

func (m *DoubleArrayAC) findAllRaw(text string) []Match {
	var out []Match
	s := 1
	for i := 0; i < len(text); i++ {
		s = m.next(s, text[i])
		// 沿 fail(字典后缀)链收集所有终结态。
		for t := s; t != 1; t = m.fail[t] {
			if m.patLen[t] > 0 {
				out = append(out, Match{
					Pattern: m.patterns[m.patID[t]],
					Start:   i - m.patLen[t] + 1,
					End:     i + 1,
				})
			}
		}
	}
	return out
}

// Contains 判断文本是否含任一模式串(短路)。
func (m *DoubleArrayAC) Contains(text string) bool {
	if m.normalize == nil {
		return m.containsRaw(text)
	}
	nb, _, _ := m.normalizeText(text)
	s := 1
	for i := 0; i < len(nb); i++ {
		s = m.next(s, nb[i])
		for t := s; t != 1; t = m.fail[t] {
			if m.patLen[t] > 0 {
				return true
			}
		}
	}
	return false
}

func (m *DoubleArrayAC) containsRaw(text string) bool {
	s := 1
	for i := 0; i < len(text); i++ {
		s = m.next(s, text[i])
		for t := s; t != 1; t = m.fail[t] {
			if m.patLen[t] > 0 {
				return true
			}
		}
	}
	return false
}

// normalizeText 把文本逐 rune 归一化成字节流,并记录每个归一化字节到原文的位置:
// startOf[k] 为产生第 k 个归一化字节的那个 rune 在原文中的起始字节下标;
// endOf[k] 为该 rune 在原文中的结束字节下标(即下一字符起点)。
// 据此可把归一化空间里的命中区间精确映射回原文字节区间。
func (m *DoubleArrayAC) normalizeText(text string) (nb []byte, startOf, endOf []int) {
	for i := 0; i < len(text); {
		r, w := utf8.DecodeRuneInString(text[i:])
		if nr := m.normalize(r); nr != 0 {
			var buf [utf8.UTFMax]byte
			k := utf8.EncodeRune(buf[:], nr)
			end := i + w
			for j := 0; j < k; j++ {
				nb = append(nb, buf[j])
				startOf = append(startOf, i)
				endOf = append(endOf, end)
			}
		}
		i += w
	}
	return nb, startOf, endOf
}

// Size 返回模式串个数。
func (m *DoubleArrayAC) Size() int { return len(m.patterns) }
