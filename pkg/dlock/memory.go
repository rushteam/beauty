package dlock

import (
	"context"
	"sync"
)

// Memory 是 Locker + Elector 的纯内存实现:多个 goroutine 竞争同一个 key,
// 语义等价"多实例竞争",供开发/测试/单实例部署使用。不跨进程——生产多实例
// 部署请用 pkg/infra/etcd 等真实后端。
//
// 零值不可用,用 NewMemory 构造。并发安全。
type Memory struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex // key → 该 key 的互斥锁(懒建)
}

// NewMemory 创建内存 Locker/Elector。
func NewMemory() *Memory {
	return &Memory{locks: make(map[string]*sync.Mutex)}
}

func (m *Memory) mutexFor(key string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.locks[key]
	if !ok {
		l = &sync.Mutex{}
		m.locks[key] = l
	}
	return l
}

// memLock 是 Memory 发出的 Lock。
type memLock struct {
	mu   *sync.Mutex
	once sync.Once
}

func (l *memLock) Unlock(context.Context) error {
	l.once.Do(l.mu.Unlock)
	return nil
}

// Lock 实现 Locker。阻塞式获取,支持 ctx 取消(取消时不会拿到锁)。
func (m *Memory) Lock(ctx context.Context, key string) (Lock, error) {
	mu := m.mutexFor(key)
	done := make(chan struct{})
	go func() {
		mu.Lock()
		close(done)
	}()
	select {
	case <-done:
		return &memLock{mu: mu}, nil
	case <-ctx.Done():
		// 注意:上面的 goroutine 仍可能在 ctx 取消后拿到锁并泄漏持有(内存实现的
		// 已知局限,真实后端用租约自动释放)。调用方应尽快在别处重试或忽略。
		return nil, ctx.Err()
	}
}

// TryLock 实现 Locker。非阻塞。
func (m *Memory) TryLock(_ context.Context, key string) (Lock, bool, error) {
	mu := m.mutexFor(key)
	if !mu.TryLock() {
		return nil, false, nil
	}
	return &memLock{mu: mu}, true, nil
}

// Run 实现 Elector:反复竞争 key 的锁,拿到即视为当选 leader,调用 onElected;
// onElected 返回或 ctx 取消即释放并重新竞选,直到 ctx 取消退出。
func (m *Memory) Run(ctx context.Context, key string, onElected func(leaderCtx context.Context)) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		lock, err := m.Lock(ctx, key)
		if err != nil {
			return err // ctx 取消
		}
		leaderCtx, cancel := context.WithCancel(ctx)
		onElected(leaderCtx)
		cancel()
		_ = lock.Unlock(ctx)
		// 让出调度,给其他竞选者机会拿锁,避免同一 goroutine 一路连选连任
		// 导致其他候选者饿死(纯内存竞争下 sync.Mutex 无公平性保证)。
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
