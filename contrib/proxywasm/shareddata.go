package proxywasm

import "sync"

// sharedDataStore 实现 Proxy-Wasm 的 Shared Data 语义。
// 提供 CAS (Compare-And-Swap) 支持的 key-value 存储，在同一 VM 的所有实例间共享。
type sharedDataStore struct {
	mu      sync.RWMutex
	entries map[string]*sharedEntry
}

type sharedEntry struct {
	data []byte
	cas  uint32
}

func newSharedDataStore() *sharedDataStore {
	return &sharedDataStore{entries: make(map[string]*sharedEntry)}
}

// Get 获取共享数据。返回 value 和当前 CAS 版本。
func (s *sharedDataStore) Get(key string) ([]byte, uint32, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.entries[key]
	if !ok {
		return nil, 0, false
	}
	cp := make([]byte, len(e.data))
	copy(cp, e.data)
	return cp, e.cas, true
}

// Set 设置共享数据。cas=0 表示无条件写入，否则必须匹配当前 CAS 版本。
// 返回是否写入成功（false 表示 CAS 不匹配）。
func (s *sharedDataStore) Set(key string, value []byte, cas uint32) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, exists := s.entries[key]
	if cas != 0 {
		if !exists || e.cas != cas {
			return false
		}
	}
	newCAS := uint32(1)
	if exists {
		newCAS = e.cas + 1
	}
	cp := make([]byte, len(value))
	copy(cp, value)
	s.entries[key] = &sharedEntry{data: cp, cas: newCAS}
	return true
}
