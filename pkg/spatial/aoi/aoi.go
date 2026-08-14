// Package aoi 在 pkg/spatial 之上提供兴趣区域(AOI)集合 diff:enter/leave/stay。
// 纯机制——不含网络同步;与 pkg/replicate 组合做增量下发。
package aoi

import (
	"github.com/rushteam/beauty/pkg/spatial"
)

// Set 记录上一帧可见实体集合,用于与当前可见集做 diff。
type Set[ID comparable] struct {
	prev map[ID]struct{}
}

// NewSet 创建空的 AOI 集合。
func NewSet[ID comparable]() *Set[ID] {
	return &Set[ID]{prev: make(map[ID]struct{})}
}

// Diff 比较当前可见实体与上一帧,返回 enter(新进入)/leave(离开)/stay(仍在视野内)。
// curr 通常来自 spatial.Index.Nearby;顺序无关。
func (s *Set[ID]) Diff(curr []spatial.Entity[ID]) (enter, leave, stay []ID) {
	currSet := make(map[ID]struct{}, len(curr))
	for _, e := range curr {
		currSet[e.ID] = struct{}{}
		if _, was := s.prev[e.ID]; was {
			stay = append(stay, e.ID)
		} else {
			enter = append(enter, e.ID)
		}
	}
	for id := range s.prev {
		if _, ok := currSet[id]; !ok {
			leave = append(leave, id)
		}
	}
	return enter, leave, stay
}

// Update 用当前可见集覆盖上一帧(在 Diff 之后调用)。
func (s *Set[ID]) Update(curr []spatial.Entity[ID]) {
	clear(s.prev)
	for _, e := range curr {
		s.prev[e.ID] = struct{}{}
	}
}

// Reset 清空历史(玩家断线重连时可调用)。
func (s *Set[ID]) Reset() {
	clear(s.prev)
}

// Len 返回上一帧记录的可见实体数。
func (s *Set[ID]) Len() int { return len(s.prev) }
