package mq

import (
	"context"
	"sync"
	"time"
)

// BatchHandler 批处理中间件:攒到 size 条消息或超时 timeout 后,将一批消息
// 一起交给 fn 处理。适合需要批量写库/批量调用外部 API 的场景,减少 I/O 次数。
//
// 借鉴 BullMQ Pro 的 Batches:将多个 Job 合并为一批统一处理。
//
// 语义:
//   - 每攒满 size 条或距上次 flush 超过 timeout,触发一次 fn;
//   - fn 返回 error 时,整批视为失败(由外层 Retry 中间件决定是否重投);
//   - ctx 取消时立即 flush 剩余消息(如果有),然后退出;
//   - 若 fn 为 nil,panic。
//
// 用法:
//
//	consumer.Handle("orders", mq.Batch(100, time.Second, func(ctx context.Context, msgs []Message) error {
//	    return db.BulkInsert(ctx, msgs)
//	}))
func Batch(size int, timeout time.Duration, fn func(ctx context.Context, msgs []Message) error) Handler {
	if fn == nil {
		panic("mq.Batch: fn must not be nil")
	}
	if size <= 0 {
		size = 1
	}
	if timeout <= 0 {
		timeout = time.Second
	}

	b := &batcher{
		size:    size,
		timeout: timeout,
		fn:      fn,
		buf:     make([]Message, 0, size),
	}
	return b.handle
}

type batcher struct {
	size    int
	timeout time.Duration
	fn      func(ctx context.Context, msgs []Message) error

	mu      sync.Mutex
	buf     []Message
	timer   *time.Timer
	flushCh chan struct{}
	once    sync.Once
}

func (b *batcher) init() {
	b.once.Do(func() {
		b.flushCh = make(chan struct{}, 1)
	})
}

func (b *batcher) handle(ctx context.Context, msg Message) error {
	b.init()
	b.mu.Lock()
	b.buf = append(b.buf, msg)
	if len(b.buf) >= b.size {
		batch := b.drain()
		b.mu.Unlock()
		return b.fn(ctx, batch)
	}
	if b.timer == nil {
		b.timer = time.AfterFunc(b.timeout, func() {
			b.mu.Lock()
			if len(b.buf) > 0 {
				batch := b.drain()
				b.mu.Unlock()
				// 超时 flush 时使用 background context(原消息的 ctx 可能已过期)
				_ = b.fn(context.Background(), batch)
			} else {
				b.mu.Unlock()
			}
		})
	}
	b.mu.Unlock()
	return nil
}

func (b *batcher) drain() []Message {
	batch := make([]Message, len(b.buf))
	copy(batch, b.buf)
	b.buf = b.buf[:0]
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	return batch
}

// BatchCollector 提供更精细控制的批处理收集器:配合 Consumer 使用,
// 在单独 goroutine 中攒批并定时 flush。
//
// 与 Batch (Handler) 的区别:
//   - Batch 是 Handler 级中间件,每条消息的 ctx 可能不同,flush 时用 background;
//   - BatchCollector 是独立组件,有自己的 Start/Stop 生命周期,flush 使用统一的 ctx,
//     适合需要精确控制 flush 时机(如 graceful shutdown 时必须 flush 完)的场景。
type BatchCollector struct {
	size    int
	timeout time.Duration
	fn      func(ctx context.Context, msgs []Message) error

	mu   sync.Mutex
	buf  []Message
	done chan struct{}
	once sync.Once
}

// NewBatchCollector 创建批处理收集器。
func NewBatchCollector(size int, timeout time.Duration, fn func(ctx context.Context, msgs []Message) error) *BatchCollector {
	if fn == nil {
		panic("mq.NewBatchCollector: fn must not be nil")
	}
	if size <= 0 {
		size = 1
	}
	if timeout <= 0 {
		timeout = time.Second
	}
	return &BatchCollector{
		size:    size,
		timeout: timeout,
		fn:      fn,
		buf:     make([]Message, 0, size),
		done:    make(chan struct{}),
	}
}

// Handler 返回一个 mq.Handler,用于注册到 Consumer。收到消息后入缓冲。
func (bc *BatchCollector) Handler() Handler {
	return func(ctx context.Context, msg Message) error {
		bc.mu.Lock()
		bc.buf = append(bc.buf, msg)
		shouldFlush := len(bc.buf) >= bc.size
		var batch []Message
		if shouldFlush {
			batch = make([]Message, len(bc.buf))
			copy(batch, bc.buf)
			bc.buf = bc.buf[:0]
		}
		bc.mu.Unlock()

		if shouldFlush {
			return bc.fn(ctx, batch)
		}
		return nil
	}
}

// Start 启动定时 flush goroutine。ctx 取消时 flush 剩余并退出。
func (bc *BatchCollector) Start(ctx context.Context) error {
	ticker := time.NewTicker(bc.timeout)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			bc.flush(ctx)
		case <-ctx.Done():
			bc.flush(context.Background()) // 优雅停机时 flush 剩余
			bc.once.Do(func() { close(bc.done) })
			return nil
		case <-bc.done:
			return nil
		}
	}
}

// Stop 停止收集器。
func (bc *BatchCollector) Stop() {
	bc.once.Do(func() { close(bc.done) })
}

// Pending 返回缓冲中待 flush 的消息数。
func (bc *BatchCollector) Pending() int {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	return len(bc.buf)
}

func (bc *BatchCollector) flush(ctx context.Context) {
	bc.mu.Lock()
	if len(bc.buf) == 0 {
		bc.mu.Unlock()
		return
	}
	batch := make([]Message, len(bc.buf))
	copy(batch, bc.buf)
	bc.buf = bc.buf[:0]
	bc.mu.Unlock()
	_ = bc.fn(ctx, batch)
}
