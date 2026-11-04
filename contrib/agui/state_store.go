package agui

import (
	"context"
	"errors"
	"sync"

	"encoding/json"
)

// ErrStateConflict 表示 StateStore 的 CAS 基线已经被另一实例更新。
var ErrStateConflict = errors.New("agui: state version conflict")

// StateRecord 是某 thread 最近一次规范化状态及其单调版本。
type StateRecord struct {
	State   json.RawMessage
	Version uint64
}

// StateStore 保存 STATE_SNAPSHOT / STATE_DELTA 的比较基线。
//
// CompareAndSwap 必须原子检查 Version 并写入 State。Redis 可用 WATCH/Lua，SQL 可用
// UPDATE ... WHERE version=?。同一 thread 的并发请求应返回 ErrStateConflict 而不是静默覆盖。
// 返回 false 的 Load 表示此前没有 snapshot。
type StateStore interface {
	Load(ctx context.Context, threadID string) (StateRecord, bool, error)
	CompareAndSwap(ctx context.Context, threadID string, previousVersion uint64, next json.RawMessage) (StateRecord, error)
}

// MemoryStateStore 是进程内 StateStore，只适合开发、测试和单副本部署。
type MemoryStateStore struct {
	mu sync.Mutex
	m  map[string]StateRecord
}

// NewMemoryStateStore 创建空内存状态库。
func NewMemoryStateStore() *MemoryStateStore {
	return &MemoryStateStore{m: make(map[string]StateRecord)}
}

func (s *MemoryStateStore) Load(_ context.Context, threadID string) (StateRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.m[threadID]
	if !ok {
		return StateRecord{}, false, nil
	}
	record.State = append(json.RawMessage(nil), record.State...)
	return record, true, nil
}

func (s *MemoryStateStore) CompareAndSwap(_ context.Context, threadID string, previousVersion uint64, next json.RawMessage) (StateRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.m[threadID]
	if exists {
		if current.Version != previousVersion {
			return StateRecord{}, ErrStateConflict
		}
	} else if previousVersion != 0 {
		return StateRecord{}, ErrStateConflict
	}
	record := StateRecord{
		State:   append(json.RawMessage(nil), next...),
		Version: previousVersion + 1,
	}
	s.m[threadID] = record
	return record, nil
}
