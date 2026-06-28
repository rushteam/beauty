// Package ephemeral 提供短期 TTL KV:不版本化、不持久,纯内存 + 到点自动过期。
//
// 与 pkg/domain/storage 互补:storage 是版本化 KV + OCC(重,用于存档/配置),
// 本包是轻量过期缓存(用于匹配房间临时数据 / 短期 token 缓存 / 排行榜快照 / 验证码)。
//
// 设计参考 Nakama 的 ephemeral storage 语义:
//   - Set(key, value, ttl):ttl 到点自动过期,Get 返回 (value, ok);
//   - 底层 map + 单 goroutine 定时清扫(参考 pkg/token 的 gc 模式);
//   - 并发安全(sync.RWMutex + channel-driven gc)。
//
// 零值不可用,用 New 构造。Store 并发安全,Stop 后清扫 goroutine 退出。
package ephemeral

import (
	"sync"
	"time"
)

// entry 一条缓存条目。
type entry struct {
	value  any
	expiry int64 // unix nano,到期点
}

// Store 短期 TTL KV 存储。
type Store struct {
	mu     sync.RWMutex
	items  map[string]entry
	stopCh chan struct{}
	stop   sync.Once
}

// Option 配置 Store。
type Option func(*config)

type config struct{}

// New 创建 Store 并启动清扫 goroutine(每分钟扫一次过期条目)。
func New(opts ...Option) *Store {
	s := &Store{
		items:  make(map[string]entry),
		stopCh: make(chan struct{}),
	}
	for _, o := range opts {
		o(&config{})
	}
	go s.gc()
	return s
}

// Set 写入 key=value,ttl 后自动过期。ttl<=0 表示立即过期(不存)。
func (s *Store) Set(key string, value any, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	expiry := time.Now().Add(ttl).UnixNano()
	s.mu.Lock()
	s.items[key] = entry{value: value, expiry: expiry}
	s.mu.Unlock()
}

// Get 读取 key,返回 (value, ok)。已过期返回 (nil, false) 并惰性删除。
func (s *Store) Get(key string) (any, bool) {
	s.mu.RLock()
	e, ok := s.items[key]
	s.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if time.Now().UnixNano() >= e.expiry {
		// 惰性删除:读到已过期就清掉。
		s.mu.Lock()
		if cur, ok := s.items[key]; ok && time.Now().UnixNano() >= cur.expiry {
			delete(s.items, key)
		}
		s.mu.Unlock()
		return nil, false
	}
	return e.value, true
}

// Delete 立即删除 key。返回是否原本存在(且未过期)。
func (s *Store) Delete(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.items[key]
	if !ok {
		return false
	}
	delete(s.items, key)
	return time.Now().UnixNano() < e.expiry
}

// Len 当前条目数(含可能未扫到的过期条目)。
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.items)
}

// Stop 停止清扫 goroutine。幂等。
func (s *Store) Stop() {
	s.stop.Do(func() { close(s.stopCh) })
}

// gc 周期清理过期条目。
func (s *Store) gc() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.mu.Lock()
			now := time.Now().UnixNano()
			for k, e := range s.items {
				if now >= e.expiry {
					delete(s.items, k)
				}
			}
			s.mu.Unlock()
		case <-s.stopCh:
			return
		}
	}
}
