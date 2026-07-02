// Package pathfind 提供网格地图上的 A* 寻路,纯计算、纯标准库、零依赖。
//
// 适用:塔防 / SLG / MMO 小地图的自动寻路、怪物追击、点击移动。把地图抽象成
// 二维网格(Grid),每格可通行或阻挡、可带移动代价(如沼泽更慢);FindPath 返回
// 从起点到终点的最短路径(格子序列)。
//
// 算法:A*(带启发式的最短路),启发函数按是否允许对角移动自动选择——
// 四方向用曼哈顿距离,八方向用对角(Chebyshev/octile)距离,保证 admissible
// (不高估),从而路径最优。开集用最小堆,闭集用 visited 标记。
//
// 网格坐标:X 为列、Y 为行,原点 (0,0) 在左上。对角移动默认关闭;开启后默认
// 禁止"穿墙角"(两侧都是障碍时不允许斜穿),可配置放开。
//
// Grid 构建后若地图不变则并发安全(只读);FindPath 无共享可变状态,可并发调用
// 同一 Grid。动态改格子(SetBlocked/SetCost)需调用方自行同步。
package pathfind

import (
	"container/heap"
	"math"
)

// Point 网格坐标(X=列, Y=行)。
type Point struct {
	X, Y int
}

// config 配置。
type config struct {
	allowDiagonal  bool
	allowCutCorner bool
}

// Option 配置寻路行为。
type Option func(*config)

// WithDiagonal 允许八方向(含对角)移动(默认仅四方向)。
func WithDiagonal(allow bool) Option { return func(c *config) { c.allowDiagonal = allow } }

// WithCutCorner 允许"贴墙角斜穿"(默认禁止:对角移动时两侧格子任一阻挡即不可斜穿)。
// 仅在开启对角移动时有意义。
func WithCutCorner(allow bool) Option { return func(c *config) { c.allowCutCorner = allow } }

// Grid 二维网格地图。每格有通行性与移动代价(>=1)。零值不可用,用 NewGrid 构造。
type Grid struct {
	w, h    int
	blocked []bool // 行主序:y*w+x
	cost    []int  // 每格进入代价(>=1),默认 1
}

// NewGrid 创建 width×height 的网格,初始全部可通行、代价 1。
func NewGrid(width, height int) *Grid {
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}
	n := width * height
	cost := make([]int, n)
	for i := range cost {
		cost[i] = 1
	}
	return &Grid{w: width, h: height, blocked: make([]bool, n), cost: cost}
}

// Width 返回网格宽(列数)。
func (g *Grid) Width() int { return g.w }

// Height 返回网格高(行数)。
func (g *Grid) Height() int { return g.h }

// InBounds 判断坐标是否在网格内。
func (g *Grid) InBounds(p Point) bool {
	return p.X >= 0 && p.X < g.w && p.Y >= 0 && p.Y < g.h
}

// SetBlocked 设置格子是否阻挡(越界忽略)。
func (g *Grid) SetBlocked(p Point, blocked bool) {
	if g.InBounds(p) {
		g.blocked[p.Y*g.w+p.X] = blocked
	}
}

// IsBlocked 返回格子是否阻挡(越界视为阻挡)。
func (g *Grid) IsBlocked(p Point) bool {
	if !g.InBounds(p) {
		return true
	}
	return g.blocked[p.Y*g.w+p.X]
}

// SetCost 设置进入某格的移动代价(<1 会被夹到 1;越界忽略)。
// 代价越高越"难走",A* 会倾向绕开(如沼泽=5、平地=1)。
func (g *Grid) SetCost(p Point, cost int) {
	if !g.InBounds(p) {
		return
	}
	if cost < 1 {
		cost = 1
	}
	g.cost[p.Y*g.w+p.X] = cost
}

// costAt 返回进入格子的代价。
func (g *Grid) costAt(p Point) int { return g.cost[p.Y*g.w+p.X] }

// node A* 开集节点。
type node struct {
	p     Point
	g     float64 // 起点到此的实际代价
	f     float64 // g + 启发
	index int
}

type openHeap []*node

func (h openHeap) Len() int           { return len(h) }
func (h openHeap) Less(i, j int) bool { return h[i].f < h[j].f }
func (h openHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i]; h[i].index = i; h[j].index = j }
func (h *openHeap) Push(x any)        { n := x.(*node); n.index = len(*h); *h = append(*h, n) }
func (h *openHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return it
}

// 四方向与八方向的邻居偏移。
var ortho = []Point{{0, -1}, {0, 1}, {-1, 0}, {1, 0}}
var diag = []Point{{-1, -1}, {1, -1}, {-1, 1}, {1, 1}}

// FindPath 求 from 到 to 的最短路径,返回从 from 到 to(含两端)的格子序列。
// 无路可达返回 nil。from/to 越界或本身阻挡则返回 nil。
func (g *Grid) FindPath(from, to Point, opts ...Option) []Point {
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}
	if !g.InBounds(from) || !g.InBounds(to) || g.IsBlocked(from) || g.IsBlocked(to) {
		return nil
	}
	if from == to {
		return []Point{from}
	}

	n := g.w * g.h
	gScore := make([]float64, n)
	for i := range gScore {
		gScore[i] = math.Inf(1)
	}
	cameFrom := make([]int, n)
	for i := range cameFrom {
		cameFrom[i] = -1
	}
	closed := make([]bool, n)

	idx := func(p Point) int { return p.Y*g.w + p.X }

	start := &node{p: from, g: 0, f: g.heuristic(from, to, cfg.allowDiagonal)}
	gScore[idx(from)] = 0
	open := &openHeap{start}
	heap.Init(open)

	for open.Len() > 0 {
		cur := heap.Pop(open).(*node)
		ci := idx(cur.p)
		if cur.p == to {
			return reconstruct(cameFrom, g.w, to, from)
		}
		if closed[ci] {
			continue // 堆里的过期副本
		}
		closed[ci] = true

		for _, nb := range g.neighbors(cur.p, cfg) {
			ni := idx(nb)
			if closed[ni] {
				continue
			}
			// 对角移动代价 ×√2,直行 ×1,再乘目标格代价。
			step := float64(g.costAt(nb))
			if nb.X != cur.p.X && nb.Y != cur.p.Y {
				step *= math.Sqrt2
			}
			tentative := gScore[ci] + step
			if tentative < gScore[ni] {
				gScore[ni] = tentative
				cameFrom[ni] = ci
				heap.Push(open, &node{p: nb, g: tentative, f: tentative + g.heuristic(nb, to, cfg.allowDiagonal)})
			}
		}
	}
	return nil // 不可达
}

// neighbors 返回可通行邻居(按配置四/八方向,处理穿墙角规则)。
func (g *Grid) neighbors(p Point, cfg config) []Point {
	out := make([]Point, 0, 8)
	for _, d := range ortho {
		np := Point{p.X + d.X, p.Y + d.Y}
		if !g.IsBlocked(np) {
			out = append(out, np)
		}
	}
	if !cfg.allowDiagonal {
		return out
	}
	for _, d := range diag {
		np := Point{p.X + d.X, p.Y + d.Y}
		if g.IsBlocked(np) {
			continue
		}
		if !cfg.allowCutCorner {
			// 禁止斜穿墙角:对角两侧的正交格任一阻挡则不可走。
			if g.IsBlocked(Point{p.X + d.X, p.Y}) || g.IsBlocked(Point{p.X, p.Y + d.Y}) {
				continue
			}
		}
		out = append(out, np)
	}
	return out
}

// heuristic 启发函数:四方向用曼哈顿,八方向用 octile 距离(均 admissible)。
func (g *Grid) heuristic(a, b Point, diagonal bool) float64 {
	dx := math.Abs(float64(a.X - b.X))
	dy := math.Abs(float64(a.Y - b.Y))
	if diagonal {
		// octile:min 边走对角(√2),剩余走直线。
		return (dx + dy) + (math.Sqrt2-2)*math.Min(dx, dy)
	}
	return dx + dy
}

// reconstruct 从 cameFrom 链回溯出 from→to 的路径。
func reconstruct(cameFrom []int, w int, to, from Point) []Point {
	var rev []Point
	cur := to.Y*w + to.X
	fromIdx := from.Y*w + from.X
	for cur != -1 {
		rev = append(rev, Point{X: cur % w, Y: cur / w})
		if cur == fromIdx {
			break
		}
		cur = cameFrom[cur]
	}
	// 反转成 from→to。
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}
