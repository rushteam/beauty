// Package fieldmask 提供字段级脏标记与增量编码原语,用于 MMO/SLG 场景下的
// 细粒度状态同步——只传输被修改的字段,而非全量实体快照。
//
// 解决的问题:一个 MMO 角色有几百个属性(血量、蓝量、坐标、装备、buff 列表…),
// 当其中一个字段改变时,如果用 replicate 的实体级 DirtySet 下发整个 EntityState,
// 会浪费大量带宽。本包在 replicate 之下提供更细的一层:字段级位图脏标记。
//
// 核心机制:
//   - Tracker[K]:为每个实体维护一个字段脏位图(bitmap);
//   - 业务调用 SetField(entity, field) 标记某实体的某个字段已改;
//   - 每帧末调用 Flush 取走所有脏实体及其脏字段列表,交给序列化层只打包这些字段。
//
// 与相邻原语的关系:
//   - replicate.DirtySet 是实体级"谁变了",fieldmask 是字段级"谁的什么变了";
//   - 业务可在 SetField 时同步调用 DirtySet.Mark,两层联动;
//   - bitmap 包提供底层位操作,fieldmask 在其上包装实体×字段的二维脏追踪;
//   - 序列化格式(protobuf FieldMask / 自定义二进制)不在本包职责内——本包只负责
//     "标脏、查脏、清脏",编码方式由业务决定。
//
// 并发安全:Tracker 用 sync.Mutex 保护(tick goroutine 写 + 出口读 Flush)。
// 零值不可用:用 NewTracker 构造。
package fieldmask

import (
	"sync"

	"github.com/rushteam/beauty/pkg/foundation/bitmap"
)

// Tracker 维护每个实体的字段级脏位图。K 是实体标识类型(string/int/…)。
// 非零值:用 NewTracker 构造。
type Tracker[K comparable] struct {
	mu      sync.Mutex
	dirty   map[K]*bitmap.Bitmap
	version map[K]uint64
}

// NewTracker 创建字段脏追踪器。
func NewTracker[K comparable]() *Tracker[K] {
	return &Tracker[K]{
		dirty:   make(map[K]*bitmap.Bitmap),
		version: make(map[K]uint64),
	}
}

// SetField 标记实体 key 的字段 field 已修改。field 是字段的数字 ID(由业务约定,
// 如 protobuf field number 或自定义的枚举常量)。
// 每次调用自动递增该实体的版本号。
func (t *Tracker[K]) SetField(key K, field int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	bm := t.dirty[key]
	if bm == nil {
		bm = bitmap.New(field + 1)
		t.dirty[key] = bm
	}
	bm.Set(field)
	t.version[key]++
}

// SetFields 批量标记多个字段。
func (t *Tracker[K]) SetFields(key K, fields ...int) {
	if len(fields) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	bm := t.dirty[key]
	if bm == nil {
		bm = bitmap.New(64)
		t.dirty[key] = bm
	}
	for _, f := range fields {
		bm.Set(f)
	}
	t.version[key]++
}

// IsDirty 检查实体 key 是否有脏字段。
func (t *Tracker[K]) IsDirty(key K) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	bm := t.dirty[key]
	return bm != nil && bm.Count() > 0
}

// IsFieldDirty 检查实体 key 的字段 field 是否脏。
func (t *Tracker[K]) IsFieldDirty(key K, field int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	bm := t.dirty[key]
	return bm != nil && bm.Test(field)
}

// Version 返回实体的当前版本号(每次 SetField/SetFields 递增)。
func (t *Tracker[K]) Version(key K) uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.version[key]
}

// Patch 是一个实体的字段级增量描述。
type Patch[K comparable] struct {
	Key     K      // 实体标识
	Fields  []int  // 本次变更的字段 ID 列表(升序)
	Version uint64 // 变更后的版本号
}

// Flush 取走并清空所有脏实体的字段列表。每帧末调用一次,返回所有需要增量同步
// 的实体及其脏字段。调用后所有脏标记被清除。
func (t *Tracker[K]) Flush() []Patch[K] {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.dirty) == 0 {
		return nil
	}
	patches := make([]Patch[K], 0, len(t.dirty))
	for key, bm := range t.dirty {
		fields := bm.Slice()
		if len(fields) == 0 {
			continue
		}
		patches = append(patches, Patch[K]{
			Key:     key,
			Fields:  fields,
			Version: t.version[key],
		})
		bm.Reset()
	}
	// 清空 dirty map(保留 version)
	clear(t.dirty)
	return patches
}

// FlushKey 取走并清空单个实体的脏字段。未修改时返回 nil。
func (t *Tracker[K]) FlushKey(key K) *Patch[K] {
	t.mu.Lock()
	defer t.mu.Unlock()
	bm := t.dirty[key]
	if bm == nil {
		return nil
	}
	fields := bm.Slice()
	if len(fields) == 0 {
		return nil
	}
	p := &Patch[K]{
		Key:     key,
		Fields:  fields,
		Version: t.version[key],
	}
	bm.Reset()
	delete(t.dirty, key)
	return p
}

// Remove 删除实体的所有追踪记录(实体销毁时调用)。
func (t *Tracker[K]) Remove(key K) {
	t.mu.Lock()
	delete(t.dirty, key)
	delete(t.version, key)
	t.mu.Unlock()
}

// Len 返回当前有脏字段的实体数量。
func (t *Tracker[K]) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.dirty)
}
