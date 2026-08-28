package dlock

import (
	"context"
	"runtime"
	"sync"
)

// Memory 是 Locker + Elector 的纯内存实现:多个 goroutine 竞争同一个 key,
// 语义等价"多实例竞争",供开发/测试/单实例部署使用。不跨进程——生产多实例
// 部署请用 pkg/infra/etcd 等真实后端。
//
// 注意:Memory 不模拟 TTL / 续租失效 / 领导权丢失;leaderCtx 仅在 outer ctx
// 取消或 onElected 返回时被 cancel。
//
// 零值不可用,用 NewMemory 构造。并发安全。
type Memory struct {
	mu    sync.Mutex
	locks map[string]*keyLock
}

// keyLock 是每个 key 的锁及其等待者队列。
type keyLock struct {
	held    bool
	waiters []chan struct{}
}

// NewMemory 创建内存 Locker/Elector。
func NewMemory() *Memory {
	return &Memory{locks: make(map[string]*keyLock)}
}

func (m *Memory) keyLockFor(key string) *keyLock {
	m.mu.Lock()
	defer m.mu.Unlock()
	kl, ok := m.locks[key]
	if !ok {
		kl = &keyLock{}
		m.locks[key] = kl
	}
	return kl
}

// memLock 是 Memory 发出的 Lock。
type memLock struct {
	m    *Memory
	key  string
	once sync.Once
}

func (l *memLock) Unlock(_ context.Context) error {
	l.once.Do(func() {
		l.m.mu.Lock()
		defer l.m.mu.Unlock()
		kl, ok := l.m.locks[l.key]
		if !ok {
			return
		}
		if len(kl.waiters) > 0 {
			// 直接将持有权转移给下一个等待者(held 保持 true)
			ch := kl.waiters[0]
			kl.waiters = kl.waiters[1:]
			close(ch)
		} else {
			kl.held = false
			delete(l.m.locks, l.key)
		}
	})
	return nil
}

// Lock 实现 Locker。阻塞式获取,支持 ctx 取消(取消时不会拿到锁)。
func (m *Memory) Lock(ctx context.Context, key string) (Lock, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	m.mu.Lock()
	kl, ok := m.locks[key]
	if !ok {
		kl = &keyLock{}
		m.locks[key] = kl
	}
	if !kl.held {
		kl.held = true
		m.mu.Unlock()
		return &memLock{m: m, key: key}, nil
	}
	// 注册等待者
	ch := make(chan struct{})
	kl.waiters = append(kl.waiters, ch)
	m.mu.Unlock()

	select {
	case <-ctx.Done():
		// 从等待者队列中移除
		m.mu.Lock()
		for i, w := range kl.waiters {
			if w == ch {
				kl.waiters = append(kl.waiters[:i], kl.waiters[i+1:]...)
				break
			}
		}
		m.mu.Unlock()
		return nil, ctx.Err()
	case <-ch:
		// 被唤醒(held 已由 Unlock 保持为 true),再次检查 ctx
		if err := ctx.Err(); err != nil {
			// ctx 已取消但持有权已转移给我们;需要释放
			lock := &memLock{m: m, key: key}
			lock.Unlock(context.WithoutCancel(ctx))
			return nil, err
		}
		return &memLock{m: m, key: key}, nil
	}
}

// TryLock 实现 Locker。非阻塞。
func (m *Memory) TryLock(ctx context.Context, key string) (Lock, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	kl, ok := m.locks[key]
	if !ok {
		kl = &keyLock{}
		m.locks[key] = kl
	}
	if kl.held {
		return nil, false, nil
	}
	kl.held = true
	return &memLock{m: m, key: key}, true, nil
}

// Run 实现 Elector:反复竞争 key 的锁,拿到即视为当选 leader,调用 onElected;
// onElected 返回或 ctx 取消即释放并重新竞选,直到 ctx 取消退出。
//
// 注意:onElected 内不得对同一 key 调用 Lock/TryLock,否则行为未定义。
func (m *Memory) Run(ctx context.Context, key string, onElected func(leaderCtx context.Context)) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		lock, err := m.Lock(ctx, key)
		if err != nil {
			return err
		}
		leaderCtx, cancel := context.WithCancel(ctx)
		func() {
			defer cancel()
			defer func() { _ = lock.Unlock(context.WithoutCancel(ctx)) }()
			defer func() { recover() }()
			onElected(leaderCtx)
		}()
		// 让出调度,给其他竞选者机会拿锁
		runtime.Gosched()
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}

var (
	_ Locker  = (*Memory)(nil)
	_ Elector = (*Memory)(nil)
)
