// Package reddot 提供小红点 / 未读聚合树:把散落各处的未读数按树形层级聚合,
// 父节点的未读 = 所有子节点未读之和,叶子清零自动向上传播。纯内存、并发安全。
//
// 解决的问题:App"我的"页那个红点,往往由 N 个来源汇总而来——未读消息 + 好友申请
// + 活动红点 + 系统通知……手写"某处清零后重算所有祖先"极易漏更或算错。reddot 用
// 一棵树建模:业务只在叶子上 Set/Clear/Incr,任意节点的聚合未读实时可查,清零沿
// 父链自动更新。
//
// 模型:节点用路径式 ID 组织成树(如 "me/msg/chat")。分两类:
//   - 叶子:直接持有未读计数(业务设置);
//   - 内部节点:计数 = 所有后代叶子之和(自动聚合,不可直接设置)。
//
// 两种未读语义:Count(精确数字,"99+")与 Dot(仅有无红点,布尔)。父节点
// Count = 子叶之和,Dot = 任一后代有未读。
//
// 树结构在首次访问路径时惰性创建。并发安全(单锁,红点树规模通常很小)。
// 零值不可用,用 New 构造。
package reddot

import (
	"sort"
	"strings"
	"sync"
)

// node 树节点。
type node struct {
	name     string
	parent   *node
	children map[string]*node
	count    int64 // 仅叶子有意义(内部节点的聚合值实时计算 / 缓存)
	isLeaf   bool
}

// Tree 小红点聚合树。零值不可用,用 New 构造。并发安全。
type Tree struct {
	mu   sync.Mutex
	root *node
	sep  string
}

// Option 配置 Tree。
type Option func(*Tree)

// WithSeparator 设置路径分隔符(默认 "/")。如 "me/msg/chat"。
func WithSeparator(sep string) Option {
	return func(t *Tree) {
		if sep != "" {
			t.sep = sep
		}
	}
}

// New 创建一棵空的小红点树。
func New(opts ...Option) *Tree {
	t := &Tree{
		root: &node{name: "", children: make(map[string]*node)},
		sep:  "/",
	}
	for _, o := range opts {
		o(t)
	}
	return t
}

// getOrCreate 沿路径取/建节点。中间节点为内部节点,末节点标记为叶子。
func (t *Tree) getOrCreate(path string, asLeaf bool) *node {
	cur := t.root
	for _, seg := range t.split(path) {
		child := cur.children[seg]
		if child == nil {
			child = &node{name: seg, parent: cur, children: make(map[string]*node)}
			cur.children[seg] = child
		}
		cur = child
	}
	if asLeaf {
		cur.isLeaf = true
	}
	return cur
}

func (t *Tree) split(path string) []string {
	path = strings.Trim(path, t.sep)
	if path == "" {
		return nil
	}
	return strings.Split(path, t.sep)
}

// find 沿路径查节点,不存在返回 nil。
func (t *Tree) find(path string) *node {
	cur := t.root
	for _, seg := range t.split(path) {
		cur = cur.children[seg]
		if cur == nil {
			return nil
		}
	}
	return cur
}

// Set 把叶子 path 的未读数设为 n(n<0 视为 0)。path 被标记为叶子。
func (t *Tree) Set(path string, n int64) {
	if n < 0 {
		n = 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.getOrCreate(path, true).count = n
}

// Incr 给叶子 path 的未读数增加 delta(结果夹在 >=0),返回增加后的值。
func (t *Tree) Incr(path string, delta int64) int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := t.getOrCreate(path, true)
	n.count = max(n.count+delta, 0)
	return n.count
}

// Clear 清零 path 子树下所有叶子的未读(等价"已读该分类")。
func (t *Tree) Clear(path string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if n := t.find(path); n != nil {
		clearSubtree(n)
	}
}

func clearSubtree(n *node) {
	n.count = 0
	for _, c := range n.children {
		clearSubtree(c)
	}
}

// Count 返回 path 节点的聚合未读数:叶子返回自身计数,内部节点返回所有后代叶子之和。
// 路径不存在返回 0。
func (t *Tree) Count(path string) int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := t.find(path)
	if n == nil {
		return 0
	}
	return aggregate(n)
}

// Dot 返回 path 是否应显示红点(聚合未读 > 0)。
func (t *Tree) Dot(path string) bool {
	return t.Count(path) > 0
}

// Total 返回整棵树的未读总数(根节点聚合)。
func (t *Tree) Total() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return aggregate(t.root)
}

// aggregate 递归求节点的聚合未读(后代叶子计数之和)。
func aggregate(n *node) int64 {
	if len(n.children) == 0 {
		return n.count // 叶子
	}
	var sum int64
	// 内部节点自身可能也带过计数(被当叶子用过又长出子节点),一并计入。
	sum += n.count
	for _, c := range n.children {
		sum += aggregate(c)
	}
	return sum
}

// Children 返回 path 直接子节点的名字及各自聚合未读(按名字排序),用于渲染分类列表。
// 路径不存在返回 nil。
func (t *Tree) Children(path string) []Entry {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := t.find(path)
	if n == nil {
		return nil
	}
	out := make([]Entry, 0, len(n.children))
	for name, c := range n.children {
		out = append(out, Entry{Name: name, Count: aggregate(c)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Entry 一个子节点的名字与聚合未读。
type Entry struct {
	Name  string
	Count int64
}
