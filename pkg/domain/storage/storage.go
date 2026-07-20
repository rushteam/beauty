// Package storage 提供版本化的对象存储原语:owner + collection + key + value + version,
// 通过乐观并发控制(OCC)保证并发写不覆盖,支持三种写语义与懒淘汰索引。
//
// 设计要点:
//   - 对象的 version = value 的 MD5 摘要,写时 If-Match 校验(乐观锁);
//   - 三种写模式:IfMatch(version 匹配才写,否则冲突)、IfNotExist(version="" 仅当不存在)、
//     LastWriteWins(version="*" 无条件 upsert);
//   - 批量写按 collection→key→owner 排序,避免循环等待死锁;
//   - 懒淘汰索引:超过 maxEntries*1.1 时按 update_time 删最旧。
//
// 适用场景:游戏存档、用户配置、任意需要"版本化 + 并发安全"的 KV 持久层缓存。
// 本包为内存实现,生产可替换 StorageBackend 接入 DB。
//
// 零值不可用,用 New 构造。Storage 并发安全。
package storage

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"
)

// Object 是一个存储对象。
type Object struct {
	OwnerID     string // 所属用户(空=公共)
	Collection  string // 集合名(逻辑分组)
	Key         string // 集合内 key
	Value       []byte // 负载
	Version     string // 当前版本(value 的 MD5;空=不存在)
	ReadAccess  int    // 0=私有 1=自己可读 2=公开可读
	WriteAccess int    // 0=只读 1=可写
	UpdateTime  int64  // unix nano
}

// WriteMode 写语义。
type WriteMode int

const (
	// WriteIfMatch:仅当传入 version 与现存版本一致时才写(OCC 乐观锁)。
	// version="" 等价于 IfNotExist(仅当对象不存在)。
	WriteIfMatch WriteMode = iota
	// WriteIfNotExist:仅当对象不存在时才写。version 参数忽略。
	WriteIfNotExist
	// WriteLastWriteWins:无条件覆盖(upsert)。
	WriteLastWriteWins
)

// WriteOp 是一次批量写操作。
type WriteOp struct {
	OwnerID     string
	Collection  string
	Key         string
	Value       []byte
	Mode        WriteMode
	Version     string // IfMatch 时的期望版本
	ReadAccess  int
	WriteAccess int
}

// ErrVersionMismatch OCC 版本冲突(他人已先写)。
var ErrVersionMismatch = errors.New("storage: version mismatch")

// ErrConflict IfNotExist 时对象已存在。
var ErrConflict = errors.New("storage: conflict (already exists)")

// ErrReadOnly 对象只读(WriteAccess=0)。
var ErrReadOnly = errors.New("storage: read-only object")

// Storage 内存对象存储,带懒淘汰索引。
type Storage struct {
	mu         sync.Mutex
	objects    map[string]*Object // key = owner/collection/key
	maxEntries int                // 懒淘汰阈值
}

// Option 配置 Storage。
type Option func(*config)

type config struct {
	maxEntries int
}

// WithMaxEntries 设置最大对象数,超出 1.1 倍时按 updateTime 删最旧(默认 10000)。
func WithMaxEntries(n int) Option { return func(c *config) { c.maxEntries = n } }

// New 创建存储。
func New(opts ...Option) *Storage {
	cfg := &config{maxEntries: 10000}
	for _, o := range opts {
		o(cfg)
	}
	return &Storage{objects: make(map[string]*Object), maxEntries: cfg.maxEntries}
}

// objectKey 拼接复合 key。
func objectKey(owner, collection, key string) string {
	return owner + "/" + collection + "/" + key
}

// versionOf 计算 value 的 MD5 作为版本号。
func versionOf(value []byte) string {
	if len(value) == 0 {
		return ""
	}
	sum := md5.Sum(value)
	return hex.EncodeToString(sum[:])
}

// Read 读取单个对象。owner="" 表示查公共对象。
// readAccess 校验:私有(0)仅 owner 可读;1 仅自己;2 任意。
func (s *Storage) Read(owner, collection, key, callerID string) (*Object, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.objects[objectKey(owner, collection, key)]
	if !ok {
		return nil, nil
	}
	if !canRead(o, callerID) {
		return nil, errors.New("storage: permission denied")
	}
	return cloneObject(o), nil
}

// Write 写入单个对象。
func (s *Storage) Write(op WriteOp, now int64) (*Object, error) {
	if now == 0 {
		now = time.Now().UnixNano()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.applyWriteLocked(op, now)
	if err != nil {
		return nil, err
	}
	s.evictLocked()
	return cloneObject(o), nil
}

// WriteBatch 批量写。按 collection→key→owner 排序后依次应用,避免死锁。
// 全部成功才提交;任一失败则已应用的回滚(事务语义)。
func (s *Storage) WriteBatch(ops []WriteOp, now int64) ([]*Object, error) {
	if now == 0 {
		now = time.Now().UnixNano()
	}
	// 排序:防止不同 caller 以不同顺序获取锁。
	sorted := make([]WriteOp, len(ops))
	copy(sorted, ops)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Collection != sorted[j].Collection {
			return sorted[i].Collection < sorted[j].Collection
		}
		if sorted[i].Key != sorted[j].Key {
			return sorted[i].Key < sorted[j].Key
		}
		return sorted[i].OwnerID < sorted[j].OwnerID
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	// 记录变更前的快照,用于回滚。
	type snapshot struct {
		key string
		obj *Object
	}
	var snaps []snapshot
	committed := make([]*Object, 0, len(sorted))
	for _, op := range sorted {
		k := objectKey(op.OwnerID, op.Collection, op.Key)
		snaps = append(snaps, snapshot{key: k, obj: s.objects[k]})
		o, err := s.applyWriteLocked(op, now)
		if err != nil {
			// 回滚已应用的。
			for _, sn := range snaps {
				if sn.obj == nil {
					delete(s.objects, sn.key)
				} else {
					s.objects[sn.key] = sn.obj
				}
			}
			return nil, err
		}
		committed = append(committed, cloneObject(o))
	}
	s.evictLocked()
	return committed, nil
}

// List 列出某 collection 下的所有对象(拷贝)。callerID 用于权限过滤。
func (s *Storage) List(collection, callerID string) []*Object {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*Object
	for _, o := range s.objects {
		if o.Collection != collection {
			continue
		}
		if !canRead(o, callerID) {
			continue
		}
		out = append(out, cloneObject(o))
	}
	return out
}

// Delete 删除对象。只读对象(WriteAccess=0)拒绝。
func (s *Storage) Delete(owner, collection, key, callerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := objectKey(owner, collection, key)
	o, ok := s.objects[k]
	if !ok {
		return nil
	}
	if !canWrite(o, callerID) {
		return ErrReadOnly
	}
	delete(s.objects, k)
	return nil
}

// Count 当前对象总数。
func (s *Storage) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.objects)
}

// applyWriteLocked 应用单次写,返回最终对象。调用方持锁。
func (s *Storage) applyWriteLocked(op WriteOp, now int64) (*Object, error) {
	k := objectKey(op.OwnerID, op.Collection, op.Key)
	existing := s.objects[k]
	// 权限:已存在对象按其 WriteAccess;新建对象用 op.WriteAccess。
	if existing != nil && !canWrite(existing, op.OwnerID) {
		return nil, ErrReadOnly
	}
	newVer := versionOf(op.Value)
	switch op.Mode {
	case WriteIfMatch:
		if op.Version == "" {
			// IfNotExist 语义。
			if existing != nil {
				return nil, ErrConflict
			}
		} else {
			if existing == nil || existing.Version != op.Version {
				return nil, ErrVersionMismatch
			}
		}
	case WriteIfNotExist:
		if existing != nil {
			return nil, ErrConflict
		}
	case WriteLastWriteWins:
		// 无条件。
	}
	o := &Object{
		OwnerID:     op.OwnerID,
		Collection:  op.Collection,
		Key:         op.Key,
		Value:       append([]byte(nil), op.Value...),
		Version:     newVer,
		ReadAccess:  op.ReadAccess,
		WriteAccess: op.WriteAccess,
		UpdateTime:  now,
	}
	s.objects[k] = o
	return o, nil
}

// evictLocked 懒淘汰:超过 maxEntries*1.1 时按 updateTime 删最旧。
func (s *Storage) evictLocked() {
	threshold := int(float64(s.maxEntries) * 1.1)
	if len(s.objects) <= threshold {
		return
	}
	// 收集所有对象,按 UpdateTime 排序,删到 maxEntries。
	type kv struct {
		key string
		t   int64
	}
	all := make([]kv, 0, len(s.objects))
	for k, o := range s.objects {
		all = append(all, kv{k, o.UpdateTime})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].t < all[j].t })
	del := len(all) - s.maxEntries
	for i := 0; i < del; i++ {
		delete(s.objects, all[i].key)
	}
}

func canRead(o *Object, callerID string) bool {
	switch o.ReadAccess {
	case 0:
		return callerID == o.OwnerID
	case 1:
		return callerID == o.OwnerID
	case 2:
		return true
	}
	return false
}

func canWrite(o *Object, callerID string) bool {
	if o.WriteAccess == 0 {
		return false // 只读:任何人都不可写
	}
	return callerID == o.OwnerID
}

func cloneObject(o *Object) *Object {
	if o == nil {
		return nil
	}
	cp := *o
	cp.Value = append([]byte(nil), o.Value...)
	return &cp
}
