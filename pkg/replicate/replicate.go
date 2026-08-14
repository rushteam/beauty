// Package replicate 提供状态同步的增量投影原语:DirtySet + AOI diff + Delta。
// 与 pkg/gameloop + pkg/spatial/aoi 组合;序列化与 entity 字段由业务决定。
package replicate

import (
	"fmt"
	"sync"

	"github.com/rushteam/beauty/pkg/spatial"
	"github.com/rushteam/beauty/pkg/spatial/aoi"
)

// EntityState 是可下发的实体快照(业务可嵌入更多字段到 Payload)。
type EntityState struct {
	ID      string  `json:"id"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	Version uint64  `json:"version"`
	Payload any     `json:"payload,omitempty"`
}

// Delta 是一帧增量(或 baseline 全量)。
type Delta struct {
	Frame    uint64        `json:"frame"`
	Baseline bool          `json:"baseline,omitempty"`
	Spawn    []EntityState `json:"spawn,omitempty"`
	Update   []EntityState `json:"update,omitempty"`
	Despawn  []string      `json:"despawn,omitempty"`
}

// DirtySet 记录本帧变更/删除的实体 ID(tick goroutine 写入,出口只读消费)。
type DirtySet[ID comparable] struct {
	mu      sync.Mutex
	dirty   map[ID]struct{}
	removed map[ID]struct{}
}

// NewDirtySet 创建 DirtySet。
func NewDirtySet[ID comparable]() *DirtySet[ID] {
	return &DirtySet[ID]{
		dirty:   make(map[ID]struct{}),
		removed: make(map[ID]struct{}),
	}
}

// Mark 标记实体在本帧有变更。
func (d *DirtySet[ID]) Mark(id ID) {
	d.mu.Lock()
	delete(d.removed, id)
	d.dirty[id] = struct{}{}
	d.mu.Unlock()
}

// Remove 标记实体已删除(本帧 despawn)。
func (d *DirtySet[ID]) Remove(id ID) {
	d.mu.Lock()
	delete(d.dirty, id)
	d.removed[id] = struct{}{}
	d.mu.Unlock()
}

// Consume 取走并清空本帧 dirty/removed 集合(每 tick 出口调用一次)。
func (d *DirtySet[ID]) Consume() (dirty, removed []ID) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for id := range d.dirty {
		dirty = append(dirty, id)
	}
	for id := range d.removed {
		removed = append(removed, id)
	}
	clear(d.dirty)
	clear(d.removed)
	return dirty, removed
}

// Lookup 返回 id 是否在本帧 dirty 集合中(Consume 前)。
func (d *DirtySet[ID]) Lookup(id ID) bool {
	d.mu.Lock()
	_, ok := d.dirty[id]
	d.mu.Unlock()
	return ok
}

// Versions 维护实体版本号(每次 Mark 时由 Replicator 递增)。
type Versions[ID comparable] struct {
	mu  sync.Mutex
	ver map[ID]uint64
}

// NewVersions 创建版本表。
func NewVersions[ID comparable]() *Versions[ID] {
	return &Versions[ID]{ver: make(map[ID]uint64)}
}

// Bump 递增并返回新版本;实体首次出现从 1 开始。
func (v *Versions[ID]) Bump(id ID) uint64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.ver[id]++
	return v.ver[id]
}

// Get 返回当前版本(不存在为 0)。
func (v *Versions[ID]) Get(id ID) uint64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.ver[id]
}

// Delete 删除实体版本记录。
func (v *Versions[ID]) Delete(id ID) {
	v.mu.Lock()
	delete(v.ver, id)
	v.mu.Unlock()
}

// Config 控制 baseline 切换阈值等。
type Config struct {
	// BaselineRatio 当 enter+leave 占当前可见集比例超过此值时发 baseline(默认 0.3)。
	BaselineRatio float64
}

func (c Config) baselineRatio() float64 {
	if c.BaselineRatio <= 0 {
		return 0.3
	}
	return c.BaselineRatio
}

// Projector 把 spatial 可见集 + dirty 投影为 per-viewer Delta。
type Projector[ID comparable] struct {
	cfg      Config
	interest map[ID]*aoi.Set[ID]
	versions *Versions[ID]
}

// NewProjector 创建投影器。viewer 为连接/玩家 ID。
func NewProjector[ID comparable](cfg Config) *Projector[ID] {
	return &Projector[ID]{
		cfg:      cfg,
		interest: make(map[ID]*aoi.Set[ID]),
		versions: NewVersions[ID](),
	}
}

// Interest 返回 viewer 的 AOI 集合(不存在则创建)。
func (p *Projector[ID]) Interest(viewer ID) *aoi.Set[ID] {
	set, ok := p.interest[viewer]
	if !ok {
		set = aoi.NewSet[ID]()
		p.interest[viewer] = set
	}
	return set
}

// DropViewer 移除 viewer 状态(断线时)。
func (p *Projector[ID]) DropViewer(viewer ID) {
	delete(p.interest, viewer)
}

// Versions 返回共享版本表(Bump 在 tick 内对 dirty 实体调用)。
func (p *Projector[ID]) Versions() *Versions[ID] { return p.versions }

// Lookup 读取实体快照;由业务在 tick 内提供。
type Lookup[ID comparable] func(id ID) (EntityState, bool)

// Project 为 viewer 生成本帧 Delta。
// visible 为 spatial.Nearby 结果;lookup 读取权威态;dirty/removed 来自 DirtySet.Consume。
func (p *Projector[ID]) Project(
	frame uint64,
	viewer ID,
	visible []spatial.Entity[ID],
	dirty []ID,
	removed []ID,
	lookup Lookup[ID],
) Delta {
	set := p.Interest(viewer)
	enter, leave, stay := set.Diff(visible)

	ratio := p.cfg.baselineRatio()
	changeN := len(enter) + len(leave)
	baseline := len(visible) > 0 && float64(changeN)/float64(len(visible)) >= ratio
	if set.Len() == 0 && len(visible) > 0 {
		baseline = true
	}

	dirtySet := toSet(dirty)
	var delta Delta
	delta.Frame = frame

	if baseline {
		delta.Baseline = true
		for _, e := range visible {
			if st, ok := lookup(e.ID); ok {
				delta.Spawn = append(delta.Spawn, st)
			}
		}
		set.Update(visible)
		return delta
	}

	for _, id := range enter {
		if st, ok := lookup(id); ok {
			delta.Spawn = append(delta.Spawn, st)
		}
	}
	for _, id := range stay {
		if _, isDirty := dirtySet[id]; !isDirty {
			continue
		}
		if st, ok := lookup(id); ok {
			delta.Update = append(delta.Update, st)
		}
	}
	for _, id := range leave {
		delta.Despawn = append(delta.Despawn, stringID(id))
		p.versions.Delete(id)
	}
	for _, id := range removed {
		delta.Despawn = append(delta.Despawn, stringID(id))
		p.versions.Delete(id)
	}
	set.Update(visible)
	return delta
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

func stringID[ID comparable](id ID) string {
	switch v := any(id).(type) {
	case string:
		return v
	default:
		return fmtString(v)
	}
}

func fmtString(v any) string {
	return fmt.Sprintf("%v", v)
}
