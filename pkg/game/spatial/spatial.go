// Package spatial 提供网格空间索引:把实体按坐标分桶到固定大小的网格单元,
// 支持"范围查询"(附近的人/实体)与 KNN(最近的 N 个),避免全量遍历。
//
// 适用:LBS「附近的人/店铺/开播」、MMO 的 AOI(兴趣区域,只把可视范围内的
// 实体变化推给玩家)、大地图分区广播、碰撞粗筛。
//
// 原理:平面按 cellSize 切成网格,每个实体落在一个单元里。查询半径 r 时只需
// 扫描"覆盖该半径的若干相邻单元"(通常 (2*ceil(r/cell)+1)^2 个),再对候选做
// 精确距离过滤——把 O(N) 全表扫描降到 O(近邻单元内实体数)。适合实体分布较均匀、
// 查询半径远小于地图尺寸的场景(绝大多数游戏/LBS)。
//
// 泛型 ID 为实体标识(comparable,如 string/int64)。坐标用 float64。
// 并发安全(单锁,读多写少;超高并发可在上层分区)。零值不可用,用 New 构造。
package spatial

import (
	"math"
	"sort"
	"sync"
)

// cellKey 网格单元坐标。
type cellKey struct {
	cx, cy int
}

// Entity 一次查询返回的实体及其坐标、到查询点的距离。
type Entity[ID comparable] struct {
	ID   ID
	X, Y float64
	Dist float64 // 到查询点的欧氏距离(Nearby/KNN 填充;其余为 0)
}

// pos 实体当前坐标。
type pos struct {
	x, y float64
}

// Index 网格空间索引。零值不可用,用 New 构造。并发安全。
type Index[ID comparable] struct {
	cellSize float64

	mu    sync.RWMutex
	cells map[cellKey]map[ID]struct{} // 单元 → 该单元内的实体集合
	ents  map[ID]pos                  // 实体 → 当前坐标(用于 Move/Remove 定位旧单元)
}

// New 创建空间索引。cellSize 为网格单元边长——建议设为"典型查询半径"量级:
// 太小则单元多、跨单元查询扫描面广;太大则单元内实体多、精确过滤成本高。
func New[ID comparable](cellSize float64) *Index[ID] {
	if cellSize <= 0 {
		cellSize = 1
	}
	return &Index[ID]{
		cellSize: cellSize,
		cells:    make(map[cellKey]map[ID]struct{}),
		ents:     make(map[ID]pos),
	}
}

func (ix *Index[ID]) cellOf(x, y float64) cellKey {
	return cellKey{
		cx: int(math.Floor(x / ix.cellSize)),
		cy: int(math.Floor(y / ix.cellSize)),
	}
}

// Add 添加或更新实体到坐标 (x,y)。已存在则等价于 Move。
func (ix *Index[ID]) Add(id ID, x, y float64) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.upsertLocked(id, x, y)
}

// Move 把实体移动到新坐标。实体不存在则新增。
func (ix *Index[ID]) Move(id ID, x, y float64) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.upsertLocked(id, x, y)
}

func (ix *Index[ID]) upsertLocked(id ID, x, y float64) {
	newCell := ix.cellOf(x, y)
	if old, ok := ix.ents[id]; ok {
		oldCell := ix.cellOf(old.x, old.y)
		if oldCell == newCell {
			ix.ents[id] = pos{x, y}
			return
		}
		ix.removeFromCellLocked(oldCell, id)
	}
	ix.ents[id] = pos{x, y}
	c := ix.cells[newCell]
	if c == nil {
		c = make(map[ID]struct{})
		ix.cells[newCell] = c
	}
	c[id] = struct{}{}
}

func (ix *Index[ID]) removeFromCellLocked(ck cellKey, id ID) {
	if c := ix.cells[ck]; c != nil {
		delete(c, id)
		if len(c) == 0 {
			delete(ix.cells, ck)
		}
	}
}

// Remove 删除实体。不存在则无操作。
func (ix *Index[ID]) Remove(id ID) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if old, ok := ix.ents[id]; ok {
		ix.removeFromCellLocked(ix.cellOf(old.x, old.y), id)
		delete(ix.ents, id)
	}
}

// Pos 返回实体当前坐标。不存在返回 (0,0,false)。
func (ix *Index[ID]) Pos(id ID) (x, y float64, ok bool) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	p, ok := ix.ents[id]
	return p.x, p.y, ok
}

// Len 返回索引中的实体总数。
func (ix *Index[ID]) Len() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.ents)
}

// Nearby 返回距 (x,y) 半径 radius 内的所有实体(含边界),按距离升序。
// exclude 中的 ID 被排除(常用于排除查询者自己)。
func (ix *Index[ID]) Nearby(x, y, radius float64, exclude ...ID) []Entity[ID] {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	out := ix.collectLocked(x, y, radius, exclude)
	sort.Slice(out, func(i, j int) bool { return out[i].Dist < out[j].Dist })
	return out
}

// KNN 返回距 (x,y) 最近的 k 个实体,按距离升序。radius 限定搜索范围
// (<=0 表示不限,但那会退化为全表扫描,建议给合理上界)。exclude 排除指定 ID。
func (ix *Index[ID]) KNN(x, y float64, k int, radius float64, exclude ...ID) []Entity[ID] {
	if k <= 0 {
		return nil
	}
	ix.mu.RLock()
	var cand []Entity[ID]
	if radius > 0 {
		cand = ix.collectLocked(x, y, radius, exclude)
	} else {
		cand = ix.collectAllLocked(x, y, exclude)
	}
	ix.mu.RUnlock()

	sort.Slice(cand, func(i, j int) bool { return cand[i].Dist < cand[j].Dist })
	if len(cand) > k {
		cand = cand[:k]
	}
	return cand
}

// collectLocked 扫描覆盖半径的相邻单元,精确过滤出半径内实体。调用方持读锁。
func (ix *Index[ID]) collectLocked(x, y, radius float64, exclude []ID) []Entity[ID] {
	ex := toSet(exclude)
	r2 := radius * radius
	span := int(math.Ceil(radius / ix.cellSize))
	center := ix.cellOf(x, y)

	var out []Entity[ID]
	for dx := -span; dx <= span; dx++ {
		for dy := -span; dy <= span; dy++ {
			c := ix.cells[cellKey{center.cx + dx, center.cy + dy}]
			for id := range c {
				if _, skip := ex[id]; skip {
					continue
				}
				p := ix.ents[id]
				ddx, ddy := p.x-x, p.y-y
				d2 := ddx*ddx + ddy*ddy
				if d2 <= r2 {
					out = append(out, Entity[ID]{ID: id, X: p.x, Y: p.y, Dist: math.Sqrt(d2)})
				}
			}
		}
	}
	return out
}

// collectAllLocked 全量收集(KNN 无 radius 时的兜底)。调用方持读锁。
func (ix *Index[ID]) collectAllLocked(x, y float64, exclude []ID) []Entity[ID] {
	ex := toSet(exclude)
	out := make([]Entity[ID], 0, len(ix.ents))
	for id, p := range ix.ents {
		if _, skip := ex[id]; skip {
			continue
		}
		ddx, ddy := p.x-x, p.y-y
		out = append(out, Entity[ID]{ID: id, X: p.x, Y: p.y, Dist: math.Sqrt(ddx*ddx + ddy*ddy)})
	}
	return out
}

func toSet[ID comparable](ids []ID) map[ID]struct{} {
	if len(ids) == 0 {
		return nil
	}
	m := make(map[ID]struct{}, len(ids))
	for _, id := range ids {
		m[id] = struct{}{}
	}
	return m
}
