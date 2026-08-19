package proxywasm

import (
	"sync"
	"sync/atomic"
)

// metricStore 管理所有定义的 metrics，在 Runtime 级别共享。
type metricStore struct {
	mu      sync.RWMutex
	metrics map[uint32]*metricEntry
	nextID  uint32
	byName  map[string]uint32
}

type metricEntry struct {
	name       string
	metricType uint32
	value      atomic.Int64
}

func newMetricStore() *metricStore {
	return &metricStore{
		metrics: make(map[uint32]*metricEntry),
		nextID:  1,
		byName:  make(map[string]uint32),
	}
}

// Define 定义一个新 metric 或返回已存在的 metric ID。
func (ms *metricStore) Define(metricType uint32, name string) uint32 {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	if id, ok := ms.byName[name]; ok {
		return id
	}
	id := ms.nextID
	ms.nextID++
	ms.metrics[id] = &metricEntry{name: name, metricType: metricType}
	ms.byName[name] = id
	return id
}

// Record 设置 metric 值（Counter/Gauge: 直接设置; Histogram: 累加观测值）。
func (ms *metricStore) Record(id uint32, value uint64) bool {
	ms.mu.RLock()
	m, ok := ms.metrics[id]
	ms.mu.RUnlock()
	if !ok {
		return false
	}
	m.value.Store(int64(value))
	return true
}

// Increment 增加 metric 值。
func (ms *metricStore) Increment(id uint32, offset int64) bool {
	ms.mu.RLock()
	m, ok := ms.metrics[id]
	ms.mu.RUnlock()
	if !ok {
		return false
	}
	m.value.Add(offset)
	return true
}

// Get 获取 metric 当前值。
func (ms *metricStore) Get(id uint32) (uint64, bool) {
	ms.mu.RLock()
	m, ok := ms.metrics[id]
	ms.mu.RUnlock()
	if !ok {
		return 0, false
	}
	return uint64(m.value.Load()), true
}

// Snapshot 导出所有 metric 名字和值（用于监控集成）。
func (ms *metricStore) Snapshot() map[string]int64 {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	out := make(map[string]int64, len(ms.metrics))
	for _, m := range ms.metrics {
		out[m.name] = m.value.Load()
	}
	return out
}
