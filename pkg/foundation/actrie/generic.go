package actrie

import (
	"sort"
	"strings"
)

// config 是 Matcher / Dict 共享的构造配置。
type config struct {
	normalize func(rune) rune // 归一化函数;nil 表示不归一化(走零分配快路径)
}

// Option 是构造选项,New / NewDict 共用。
type Option func(*config)

// WithNormalizer 设置 rune 级归一化函数,在 Add 与查询时对每个字符生效。
// 返回 0 表示跳过该字符(不参与匹配)。用于大小写折叠、视觉混淆映射、剥离干扰符等反绕过。
func WithNormalizer(fn func(rune) rune) Option {
	return func(c *config) { c.normalize = fn }
}

// dnode 是泛型 AC 自动机的一个状态,终结状态携带原始词与 payload。
type dnode[T any] struct {
	children map[rune]*dnode[T]
	fail     *dnode[T]
	depth    int  // 该状态对应前缀的(归一化后)rune 长度
	term     bool // 是否为某模式串结尾
	word     string
	payload  T
	dictFail *dnode[T] // 字典后缀链:枚举以当前状态为后缀的所有词尾
}

func newDNode[T any]() *dnode[T] {
	return &dnode[T]{children: make(map[rune]*dnode[T])}
}

// Dict 是"每个模式串携带一个泛型 payload T"的 AC 自动机——命中时不仅知道匹配了哪个词,
// 还能拿到该词关联的元数据(分类、等级、来源用户、替换文案……),避免命中后再查一次表。
//
// 典型:敏感词分级(政治/广告 + 级别 → 不同遮罩)、多租户词库(同词不同归属)、
// 关键词路由(词 → 处理器)。字符串版 Matcher 即 Dict[struct{}] 的外观。
//
// 并发安全:Build 后查询只读,可并发;构建期非并发安全。零值不可用,用 NewDict 构造。
type Dict[T any] struct {
	cfg   config
	root  *dnode[T]
	size  int
	built bool
}

// NewDict 创建空的泛型词典自动机。
func NewDict[T any](opts ...Option) *Dict[T] {
	d := &Dict[T]{root: newDNode[T]()}
	for _, opt := range opts {
		opt(&d.cfg)
	}
	return d
}

// Add 添加一个模式串及其 payload。空串(或归一化后为空)忽略。
// 重复词后者覆盖前者的 payload(size 不变)。Build 后再 Add 需重新 Build。
func (d *Dict[T]) Add(word string, payload T) {
	if word == "" {
		return
	}
	d.built = false
	cur := d.root
	added := false
	for _, r := range word {
		nr := r
		if d.cfg.normalize != nil {
			nr = d.cfg.normalize(r)
			if nr == 0 {
				continue
			}
		}
		added = true
		nxt, ok := cur.children[nr]
		if !ok {
			nxt = newDNode[T]()
			cur.children[nr] = nxt
			nxt.depth = cur.depth + 1
		}
		cur = nxt
	}
	if !added {
		return
	}
	if !cur.term {
		d.size++
		cur.term = true
	}
	cur.word = word
	cur.payload = payload
}

// Size 返回已添加的(去重后)模式串个数。
func (d *Dict[T]) Size() int { return d.size }

// Build 用 BFS 构建 fail 指针与字典后缀链。查询会自动调用。
func (d *Dict[T]) Build() {
	queue := make([]*dnode[T], 0, 64)
	for _, child := range d.root.children {
		child.fail = d.root
		queue = append(queue, child)
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for r, child := range cur.children {
			f := cur.fail
			for f != nil && f.children[r] == nil {
				f = f.fail
			}
			if f == nil {
				child.fail = d.root
			} else {
				child.fail = f.children[r]
			}
			if child.fail.term {
				child.dictFail = child.fail
			} else {
				child.dictFail = child.fail.dictFail
			}
			queue = append(queue, child)
		}
	}
	d.built = true
}

// DictMatch 表示一次命中:原始词、其 payload,以及在原文本中的 rune 起止下标 [Start, End)。
type DictMatch[T any] struct {
	Word    string
	Payload T
	Start   int // 原文 rune 下标(非 byte)
	End     int // 原文 rune 下标(不含)
}

func (d *Dict[T]) step(cur *dnode[T], r rune) *dnode[T] {
	for cur != d.root && cur.children[r] == nil {
		cur = cur.fail
	}
	if nxt, ok := cur.children[r]; ok {
		return nxt
	}
	return cur
}

// normalizeText 归一化文本,返回归一化后的 rune 序列与各自对应的原文 rune 下标。
func (d *Dict[T]) normalizeText(text string) (runes []rune, orig []int) {
	ri := 0
	for _, r := range text {
		nr := d.cfg.normalize(r)
		if nr != 0 {
			runes = append(runes, nr)
			orig = append(orig, ri)
		}
		ri++
	}
	return
}

// walk 是统一扫描核心:对每个命中调用 onMatch(终结节点, 原文起, 原文止),返回 false 提前停止。
func (d *Dict[T]) walk(text string, onMatch func(n *dnode[T], start, end int) bool) {
	if !d.built {
		d.Build()
	}
	cur := d.root
	if d.cfg.normalize == nil {
		i := 0
		for _, r := range text {
			cur = d.step(cur, r)
			for n := cur; n != nil; n = n.dictFail {
				if n.term {
					if !onMatch(n, i+1-n.depth, i+1) {
						return
					}
				}
			}
			i++
		}
		return
	}
	runes, orig := d.normalizeText(text)
	for j := 0; j < len(runes); j++ {
		cur = d.step(cur, runes[j])
		for n := cur; n != nil; n = n.dictFail {
			if n.term {
				start := orig[j+1-n.depth]
				end := orig[j] + 1
				if !onMatch(n, start, end) {
					return
				}
			}
		}
	}
}

// FindAll 返回全部命中(含重叠),按结束位置升序。
func (d *Dict[T]) FindAll(text string) []DictMatch[T] {
	var out []DictMatch[T]
	d.walk(text, func(n *dnode[T], start, end int) bool {
		out = append(out, DictMatch[T]{Word: n.word, Payload: n.payload, Start: start, End: end})
		return true
	})
	return out
}

// Contains 判断是否含任一模式串(短路)。无归一化时零内存分配。
func (d *Dict[T]) Contains(text string) bool {
	if d.cfg.normalize == nil {
		if !d.built {
			d.Build()
		}
		cur := d.root
		for _, r := range text {
			cur = d.step(cur, r)
			if cur.term || cur.dictFail != nil {
				return true
			}
		}
		return false
	}
	found := false
	d.walk(text, func(*dnode[T], int, int) bool { found = true; return false })
	return found
}

// FindFirst 返回第一个命中(结束位置最早),没有则 ok=false。
func (d *Dict[T]) FindFirst(text string) (DictMatch[T], bool) {
	var res DictMatch[T]
	found := false
	d.walk(text, func(n *dnode[T], start, end int) bool {
		res = DictMatch[T]{Word: n.word, Payload: n.payload, Start: start, End: end}
		found = true
		return false
	})
	return res, found
}

// Count 统计每个模式串出现次数(含重叠),按原始词聚合。
func (d *Dict[T]) Count(text string) map[string]int {
	out := make(map[string]int)
	d.walk(text, func(n *dnode[T], _, _ int) bool {
		out[n.word]++
		return true
	})
	return out
}

// Replace 把所有命中替换为等长 mask 字符,重叠命中合并为连续遮罩。
func (d *Dict[T]) Replace(text string, mask rune) string {
	runes := []rune(text)
	matches := d.FindAll(text)
	if len(matches) == 0 {
		return text
	}
	masked := make([]bool, len(runes))
	for _, mt := range matches {
		for i := mt.Start; i < mt.End && i < len(masked); i++ {
			masked[i] = true
		}
	}
	var b strings.Builder
	b.Grow(len(text))
	for i, r := range runes {
		if masked[i] {
			b.WriteRune(mask)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ReplaceFunc 用回调决定每个命中的替换文本(可据 payload 做分级遮罩、打标、脱敏)。
// 命中按起点升序、同起点长者优先,与已替换区间重叠者跳过(最长/最左优先)。替换文本可不等长。
func (d *Dict[T]) ReplaceFunc(text string, repl func(DictMatch[T]) string) string {
	matches := d.FindAll(text)
	if len(matches) == 0 {
		return text
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Start != matches[j].Start {
			return matches[i].Start < matches[j].Start
		}
		return matches[i].End > matches[j].End
	})
	runes := []rune(text)
	var b strings.Builder
	b.Grow(len(text))
	cursor := 0
	for _, mt := range matches {
		if mt.Start < cursor {
			continue
		}
		b.WriteString(string(runes[cursor:mt.Start]))
		b.WriteString(repl(mt))
		cursor = mt.End
	}
	b.WriteString(string(runes[cursor:]))
	return b.String()
}
