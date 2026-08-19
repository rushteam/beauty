// Package kvstore 定义一个带 TTL 的原子键值存储接口,供需要"跨进程共享状态"的
// 原语(counter / cooldown / idempotency)接入分布式后端(如 Redis)。
//
// 背景:这些原语的默认实现是纯内存、单进程的——状态不跨实例、进程重启即丢。
// 单实例部署没问题,但多实例水平扩展时,同一 key 的状态散落在各实例内存里会
// 出错(配额可被绕过、幂等失效、冷却各算各的)。把状态存取抽象成 Store 接口后,
// 生产环境可用 Redis 等共享后端实现,让状态真正跨实例一致。
//
// 设计:接口方法都是原子操作(Incr / SetNX / TTL 等),context 感知、返回 error
// (网络调用会失败)。原语默认仍走内存,不配置 Store 时零开销、行为不变;配置
// Store(WithStore)后所有状态操作路由到后端,store 错误经各原语的 WithOnStoreError
// 钩子上报并安全降级。
//
// 本包只定义接口 + 提供内存实现 Memory(供测试/单机用)。真实后端(Redis)由使用方
// 实现——见各方法注释里标注的对应 Redis 命令。遵循 beauty 纯标准库约定,不引入
// 任何后端 SDK 依赖。
package kvstore

import (
	"context"
	"sync"
	"time"
)

// Store 是带 TTL 的原子键值存储。所有方法应为原子操作,并发安全。
// 值以 []byte 存储;计数场景用 Incr(后端应保证原子自增)。
type Store interface {
	// Incr 原子地给 key 增加 delta;key 不存在时创建并设置 ttl(已存在则不刷新 ttl)。
	// 返回增加后的值。对应 Redis: INCRBY + (首次) EXPIRE。
	Incr(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error)

	// GetInt 读取 key 的整数值。不存在/已过期返回 (0, false, nil)。
	// 对应 Redis: GET(解析为 int64)。
	GetInt(ctx context.Context, key string) (int64, bool, error)

	// Get 读取 key 的原始字节。不存在/已过期返回 (nil, false, nil)。对应 Redis: GET。
	Get(ctx context.Context, key string) ([]byte, bool, error)

	// Set 无条件写入 key=val 并设置 ttl(ttl<=0 表示不过期)。对应 Redis: SET [EX]。
	Set(ctx context.Context, key string, val []byte, ttl time.Duration) error

	// SetNX 仅当 key 不存在时写入 val + ttl,返回是否写入成功。
	// 冷却触发、幂等占位都靠它做"抢占"。对应 Redis: SET NX [EX]。
	SetNX(ctx context.Context, key string, val []byte, ttl time.Duration) (bool, error)

	// TTL 返回 key 的剩余存活时间。不存在返回 (0, false, nil);永不过期返回一个
	// 很大的值 + true。对应 Redis: PTTL。
	TTL(ctx context.Context, key string) (time.Duration, bool, error)

	// Delete 删除 key(不存在也不报错)。对应 Redis: DEL。
	Delete(ctx context.Context, key string) error
}

// ===== 内存实现 =====

type entry struct {
	val    []byte
	num    int64
	isNum  bool
	expiry int64 // unix nano;0 表示永不过期
}

// Memory 是 Store 的纯内存实现(TTL + 惰性/周期清理),用于单机与测试。
// 与各原语自带的内存实现等价,主要价值是"用统一 Store 接口跑通链路"。
// 零值不可用,用 NewMemory 构造;Stop 后 gc goroutine 退出。
type Memory struct {
	mu     sync.Mutex
	items  map[string]*entry
	stopCh chan struct{}
	stop   sync.Once
}

// NewMemory 创建内存 Store 并启动周期清理(每分钟)。
func NewMemory() *Memory {
	m := &Memory{items: make(map[string]*entry), stopCh: make(chan struct{})}
	go m.gc()
	return m
}

func (m *Memory) alive(e *entry, now int64) bool {
	return e.expiry == 0 || now < e.expiry
}

// Incr 实现 Store。
func (m *Memory) Incr(_ context.Context, key string, delta int64, ttl time.Duration) (int64, error) {
	now := time.Now().UnixNano()
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.items[key]
	if !ok || !m.alive(e, now) {
		exp := int64(0)
		if ttl > 0 {
			exp = now + int64(ttl)
		}
		e = &entry{isNum: true, num: 0, expiry: exp}
		m.items[key] = e
	}
	e.isNum = true
	e.num += delta
	return e.num, nil
}

// GetInt 实现 Store。
func (m *Memory) GetInt(_ context.Context, key string) (int64, bool, error) {
	now := time.Now().UnixNano()
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.items[key]
	if !ok || !m.alive(e, now) {
		return 0, false, nil
	}
	return e.num, true, nil
}

// Get 实现 Store。
func (m *Memory) Get(_ context.Context, key string) ([]byte, bool, error) {
	now := time.Now().UnixNano()
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.items[key]
	if !ok || !m.alive(e, now) {
		return nil, false, nil
	}
	cp := make([]byte, len(e.val))
	copy(cp, e.val)
	return cp, true, nil
}

// Set 实现 Store。
func (m *Memory) Set(_ context.Context, key string, val []byte, ttl time.Duration) error {
	now := time.Now().UnixNano()
	exp := int64(0)
	if ttl > 0 {
		exp = now + int64(ttl)
	}
	cp := make([]byte, len(val))
	copy(cp, val)
	m.mu.Lock()
	m.items[key] = &entry{val: cp, expiry: exp}
	m.mu.Unlock()
	return nil
}

// SetNX 实现 Store。
func (m *Memory) SetNX(_ context.Context, key string, val []byte, ttl time.Duration) (bool, error) {
	now := time.Now().UnixNano()
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.items[key]; ok && m.alive(e, now) {
		return false, nil
	}
	exp := int64(0)
	if ttl > 0 {
		exp = now + int64(ttl)
	}
	cp := make([]byte, len(val))
	copy(cp, val)
	m.items[key] = &entry{val: cp, expiry: exp}
	return true, nil
}

// TTL 实现 Store。
func (m *Memory) TTL(_ context.Context, key string) (time.Duration, bool, error) {
	now := time.Now().UnixNano()
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.items[key]
	if !ok || !m.alive(e, now) {
		return 0, false, nil
	}
	if e.expiry == 0 {
		return time.Duration(1<<62 - 1), true, nil // 近似"永不过期"
	}
	return time.Duration(e.expiry - now), true, nil
}

// Delete 实现 Store。
func (m *Memory) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	delete(m.items, key)
	m.mu.Unlock()
	return nil
}

// Stop 停止清理 goroutine。幂等。
func (m *Memory) Stop() { m.stop.Do(func() { close(m.stopCh) }) }

func (m *Memory) gc() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			now := time.Now().UnixNano()
			m.mu.Lock()
			for k, e := range m.items {
				if e.expiry != 0 && now >= e.expiry {
					delete(m.items, k)
				}
			}
			m.mu.Unlock()
		case <-m.stopCh:
			return
		}
	}
}

var _ Store = (*Memory)(nil)
