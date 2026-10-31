// Package bulkhead 提供「隔离舱」(Bulkhead)并发隔离:给每一类调用划一个独立的并发配额,
// 像轮船的水密隔舱——某个依赖变慢/打满,只会把自己这一舱的配额耗尽,不会把调用方的所有
// goroutine 都堵在那个慢依赖上拖垮整个进程(线程/协程耗尽型雪崩)。
//
// 与相邻机制分工:
//   - ratelimit 限的是「单位时间的速率」(QPS),bulkhead 限的是「同一时刻的并发数」(in-flight);
//   - circuitbreaker 在错误率高时整体熔断,bulkhead 不看错误、只隔离资源占用;
//   - 三者常叠加:每个下游一个 bulkhead,配额满即快速失败(ErrFull),避免无界排队。
//
// 可选「排队」:并发满时允许最多 maxQueue 个调用等待空位(默认 0,即满了立刻失败);
// 等待可被 context 取消,也可设获取超时。纯标准库,并发安全。零值不可用,用 New 构造。
package bulkhead

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

// ErrFull 表示隔离舱并发已满且等待队列也满(或不允许等待),调用被快速拒绝。
var ErrFull = errors.New("bulkhead: capacity and queue full")

// ErrAcquireTimeout 表示在获取并发槽位时等待超时。
var ErrAcquireTimeout = errors.New("bulkhead: acquire timed out")

// Bulkhead 是一个并发隔离舱。
type Bulkhead struct {
	slots    chan struct{} // 容量 = 最大并发数,占位即持有一个槽
	maxQueue int           // 允许的最大等待者数(不含正在执行者)
	maxWait  time.Duration // 获取槽位的最长等待(<=0 表示不额外限时,仍受 ctx 约束)
	waiting  atomic.Int64  // 当前等待者数
	inFlight atomic.Int64  // 当前执行中的数
	rejected atomic.Int64  // 累计被拒绝数
}

// Option 配置 Bulkhead。
type Option func(*Bulkhead)

// WithQueue 设置并发满时允许的最大等待者数(默认 0:满了立即 ErrFull)。
func WithQueue(n int) Option {
	return func(b *Bulkhead) {
		if n > 0 {
			b.maxQueue = n
		}
	}
}

// WithMaxWait 设置获取槽位的最长等待时长(默认 0:仅受传入 ctx 约束)。
func WithMaxWait(d time.Duration) Option {
	return func(b *Bulkhead) { b.maxWait = d }
}

// New 创建最大并发为 maxConcurrent 的隔离舱(<=0 夹到 1)。
func New(maxConcurrent int, opts ...Option) *Bulkhead {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	b := &Bulkhead{slots: make(chan struct{}, maxConcurrent)}
	for _, o := range opts {
		o(b)
	}
	return b
}

// Acquire 获取一个并发槽位,返回释放函数。并发已满时:若等待队列有空位则阻塞等待
// (受 ctx、maxWait 约束),否则立即返回 ErrFull。务必在拿到槽位后 defer release()。
func (b *Bulkhead) Acquire(ctx context.Context) (release func(), err error) {
	// 快速路径:有空位直接拿。
	select {
	case b.slots <- struct{}{}:
		b.inFlight.Add(1)
		return b.releaser(), nil
	default:
	}

	// 并发已满:判断是否允许排队。
	if b.maxQueue <= 0 || int(b.waiting.Load()) >= b.maxQueue {
		b.rejected.Add(1)
		return nil, ErrFull
	}

	b.waiting.Add(1)
	defer b.waiting.Add(-1)

	var timeout <-chan time.Time
	if b.maxWait > 0 {
		t := time.NewTimer(b.maxWait)
		defer t.Stop()
		timeout = t.C
	}

	select {
	case b.slots <- struct{}{}:
		b.inFlight.Add(1)
		return b.releaser(), nil
	case <-ctx.Done():
		b.rejected.Add(1)
		return nil, ctx.Err()
	case <-timeout:
		b.rejected.Add(1)
		return nil, ErrAcquireTimeout
	}
}

// releaser 返回一个幂等的释放函数。
func (b *Bulkhead) releaser() func() {
	var done atomic.Bool
	return func() {
		if done.CompareAndSwap(false, true) {
			b.inFlight.Add(-1)
			<-b.slots
		}
	}
}

// Do 在隔离舱内执行 fn:先获取槽位(失败则直接返回 ErrFull/超时/ctx 错误,不执行 fn),
// 执行完自动释放。
func (b *Bulkhead) Do(ctx context.Context, fn func() error) error {
	release, err := b.Acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	return fn()
}

// InFlight 返回当前正在执行的数量。
func (b *Bulkhead) InFlight() int { return int(b.inFlight.Load()) }

// Waiting 返回当前在排队等待槽位的数量。
func (b *Bulkhead) Waiting() int { return int(b.waiting.Load()) }

// Available 返回当前空闲槽位数。
func (b *Bulkhead) Available() int { return cap(b.slots) - int(b.inFlight.Load()) }

// Rejected 返回累计被拒绝(ErrFull/超时/取消)的次数。
func (b *Bulkhead) Rejected() int64 { return b.rejected.Load() }
