package hls

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Store 是分片字节的可插拔存储后端。Stream 只在内存里维护播放列表元数据(序号+时长),
// 分片内容的存/取/淘汰交给 Store——从而支持内存、磁盘、对象存储等不同落地。
//
// 实现须并发安全(Append 来自采集 goroutine,Get 来自多个 HTTP 请求)。
type Store interface {
	Put(seq uint64, data []byte) error    // 写入一个分片
	Get(seq uint64) ([]byte, bool, error) // 读取;不存在返回 (nil,false,nil)
	Remove(seq uint64) error              // 淘汰(不存在也不报错)
}

// ===== 内存实现(默认)=====

// MemoryStore 把分片存在内存 map 里,进程退出即丢。适合直播滚动窗口(总量有界)。
type MemoryStore struct {
	mu   sync.RWMutex
	data map[uint64][]byte
}

// NewMemoryStore 创建内存 Store。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{data: make(map[uint64][]byte)}
}

func (m *MemoryStore) Put(seq uint64, data []byte) error {
	b := append([]byte(nil), data...) // 拷贝,避免调用方复用底层数组
	m.mu.Lock()
	m.data[seq] = b
	m.mu.Unlock()
	return nil
}

func (m *MemoryStore) Get(seq uint64) ([]byte, bool, error) {
	m.mu.RLock()
	b, ok := m.data[seq]
	m.mu.RUnlock()
	return b, ok, nil
}

func (m *MemoryStore) Remove(seq uint64) error {
	m.mu.Lock()
	delete(m.data, seq)
	m.mu.Unlock()
	return nil
}

// ===== 磁盘实现 =====

// DiskStore 把分片写到目录下的文件(seg{seq}.dat),重启后仍在(可做简单持久化/大窗口)。
// 淘汰即删文件。文件名与播放列表 URL 无关(HTTP 侧按 seq 经 Store 取,不直接暴露路径)。
type DiskStore struct {
	dir string
}

// NewDiskStore 创建磁盘 Store,分片落在 dir(不存在则创建)。
func NewDiskStore(dir string) (*DiskStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("hls diskstore: mkdir %s: %w", dir, err)
	}
	return &DiskStore{dir: dir}, nil
}

func (d *DiskStore) path(seq uint64) string {
	return filepath.Join(d.dir, fmt.Sprintf("seg%d.dat", seq))
}

func (d *DiskStore) Put(seq uint64, data []byte) error {
	if err := os.WriteFile(d.path(seq), data, 0o644); err != nil {
		return fmt.Errorf("hls diskstore: write seg %d: %w", seq, err)
	}
	return nil
}

func (d *DiskStore) Get(seq uint64) ([]byte, bool, error) {
	b, err := os.ReadFile(d.path(seq))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("hls diskstore: read seg %d: %w", seq, err)
	}
	return b, true, nil
}

func (d *DiskStore) Remove(seq uint64) error {
	if err := os.Remove(d.path(seq)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("hls diskstore: remove seg %d: %w", seq, err)
	}
	return nil
}
