// Package keyedmutex 提供按 key 的细粒度锁:同一 key 的持有者互斥(串行),
// 不同 key 之间互不阻塞(并行)。纯内存、并发安全。
//
// 解决的问题:很多结构用"一把大锁锁整个 map"来保证并发安全,但这让不相关的
// 实体也相互阻塞——改玩家 A 的存档不该挡住改玩家 B 的。keyedmutex 让粒度落到
// 单个实体:Lock("player:A") 只和其他 "player:A" 的持有者互斥。典型:
// 同一账户的扣款串行、同一房间的状态变更串行、同一订单的处理串行。
//
// 与 pkg/idempotency 的区别:idempotency 是"同 key 只执行一次并复用结果";
// keyedmutex 是"同 key 串行执行每一次"(每次都跑,只是不并发)。
//
// 内存管理:锁按需创建,持有计数归零时自动从 map 删除,不会随 key 增长而泄漏。
// 用引用计数(waiters + holder)保证"正在被等待/持有的锁"不被误删。
//
// 零值不可用,用 New 构造。
package keyedmutex

import "sync"

// entry 单个 key 的锁及其引用计数。
type entry struct {
	mu  sync.Mutex // 该 key 的实际互斥锁
	ref int        // 引用计数:等待 + 持有该锁的 goroutine 数;归零则回收 entry
}

// KeyedMutex 按 key 的互斥锁集合。零值不可用,用 New 构造。并发安全。
type KeyedMutex struct {
	mu      sync.Mutex // 保护 entries map
	entries map[string]*entry
}

// New 创建一个 KeyedMutex。
func New() *KeyedMutex {
	return &KeyedMutex{entries: make(map[string]*entry)}
}

// acquireEntry 取/建 key 的 entry 并 ref++(持 km.mu)。
func (km *KeyedMutex) acquireEntry(key string) *entry {
	km.mu.Lock()
	e, ok := km.entries[key]
	if !ok {
		e = &entry{}
		km.entries[key] = e
	}
	e.ref++
	km.mu.Unlock()
	return e
}

// releaseEntry ref-- 并在归零时回收(持 km.mu)。
func (km *KeyedMutex) releaseEntry(key string, e *entry) {
	km.mu.Lock()
	e.ref--
	if e.ref == 0 {
		// 仍是同一个 entry 才删(防御性:map 里可能已被替换)。
		if cur, ok := km.entries[key]; ok && cur == e {
			delete(km.entries, key)
		}
	}
	km.mu.Unlock()
}

// Lock 获取 key 的锁,阻塞直到可用。返回一个 unlock 函数——调用它释放锁并
// 递减引用计数(而非再调 Unlock(key)),避免 double-unlock 与配对错误:
//
//	unlock := km.Lock("player:1")
//	defer unlock()
//	// ... 临界区 ...
func (km *KeyedMutex) Lock(key string) (unlock func()) {
	e := km.acquireEntry(key)
	e.mu.Lock()

	var once sync.Once
	return func() {
		once.Do(func() {
			e.mu.Unlock()
			km.releaseEntry(key, e)
		})
	}
}

// TryLock 尝试获取 key 的锁,不阻塞。成功返回 (unlock, true),失败返回 (nil, false)。
func (km *KeyedMutex) TryLock(key string) (unlock func(), ok bool) {
	e := km.acquireEntry(key)
	if !e.mu.TryLock() {
		km.releaseEntry(key, e) // 没拿到,撤销引用
		return nil, false
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			e.mu.Unlock()
			km.releaseEntry(key, e)
		})
	}, true
}

// Do 在持有 key 锁的情况下执行 fn(便捷封装,自动释放)。
// fn 内的 panic 会正常向上传播,锁仍会被释放(defer)。
func (km *KeyedMutex) Do(key string, fn func()) {
	unlock := km.Lock(key)
	defer unlock()
	fn()
}

// Len 返回当前活跃(被持有或等待中)的 key 数。主要用于测试/观测——
// 空闲后应回落到 0。
func (km *KeyedMutex) Len() int {
	km.mu.Lock()
	defer km.mu.Unlock()
	return len(km.entries)
}
