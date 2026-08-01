// Package throttle 提供批量聚合触发原语:攒满 N 条或到达 T 时间即批量 flush。
//
// 典型场景:
//   - 日志/事件批量写入(攒够 100 条或每秒 flush 一次)
//   - 消息推送批量合并(减少网络往返)
//   - 数据库批量 INSERT(攒 batch 提升吞吐)
//
// 与 chanx/stream 区别: throttle 是"攒批 + 定时触发"的专用原语,
// 不做扇出/路由/背压,只关注"何时 flush"。
//
// 纯标准库、并发安全。
package throttle

import (
	"context"
	"sync"
	"time"
)

// FlushFunc 批量处理回调。items 为攒满的一批数据。
type FlushFunc[T any] func(items []T)

// Option 配置 Throttle。
type Option func(*config)

type config struct {
	maxBatch int
	interval time.Duration
}

// WithMaxBatch 设置触发 flush 的最大批量大小(默认 100)。
func WithMaxBatch(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.maxBatch = n
		}
	}
}

// WithInterval 设置定时 flush 间隔(默认 1s)。到达间隔时间未满 batch 也 flush。
func WithInterval(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.interval = d
		}
	}
}

// Throttle 批量聚合触发器。并发安全。
// 调用 Add 添加数据;数据攒满 maxBatch 或间隔到达时触发 flushFn。
// 需调用 Start 启动定时器,Stop 停止并 flush 剩余。
type Throttle[T any] struct {
	cfg     config
	flushFn FlushFunc[T]

	mu      sync.Mutex
	flushMu sync.Mutex // 串行化所有 flushFn 调用
	buf     []T
	cancel  context.CancelFunc
	done    chan struct{}
}

// New 创建 Throttle。flushFn 在触发时被调用(可能来自 Add 的 goroutine 或定时器 goroutine)。
func New[T any](flushFn FlushFunc[T], opts ...Option) *Throttle[T] {
	cfg := config{
		maxBatch: 100,
		interval: time.Second,
	}
	for _, o := range opts {
		o(&cfg)
	}
	return &Throttle[T]{
		cfg:     cfg,
		flushFn: flushFn,
		buf:     make([]T, 0, cfg.maxBatch),
	}
}

// Start 启动定时 flush。必须调用一次;重复调用无效。
func (t *Throttle[T]) Start(ctx context.Context) {
	t.mu.Lock()
	if t.done != nil {
		t.mu.Unlock()
		return
	}
	childCtx, cancel := context.WithCancel(ctx)
	t.cancel = cancel
	t.done = make(chan struct{})
	t.mu.Unlock()

	go t.loop(childCtx)
}

// Stop 停止定时器并 flush 剩余数据。阻塞直到最后一次 flush 完成。
func (t *Throttle[T]) Stop() {
	t.mu.Lock()
	if t.cancel == nil {
		t.mu.Unlock()
		return
	}
	cancel := t.cancel
	done := t.done
	t.mu.Unlock()

	cancel()
	<-done

	t.mu.Lock()
	remaining := t.swapBuf()
	t.mu.Unlock()
	if len(remaining) > 0 {
		t.flushMu.Lock()
		t.flushFn(remaining)
		t.flushMu.Unlock()
	}
}

// Add 添加一条数据。如果攒满 maxBatch 则立即触发 flush。
func (t *Throttle[T]) Add(item T) {
	t.mu.Lock()
	t.buf = append(t.buf, item)
	if len(t.buf) >= t.cfg.maxBatch {
		batch := t.swapBuf()
		t.mu.Unlock()
		t.flushMu.Lock()
		t.flushFn(batch)
		t.flushMu.Unlock()
		return
	}
	t.mu.Unlock()
}

// AddBatch 添加多条数据。可能触发一次或多次 flush。
func (t *Throttle[T]) AddBatch(items []T) {
	for _, item := range items {
		t.Add(item)
	}
}

// Len 返回当前缓冲区中的待 flush 数据量。
func (t *Throttle[T]) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.buf)
}

// Flush 手动触发一次 flush(无论缓冲区是否达到阈值)。
func (t *Throttle[T]) Flush() {
	t.mu.Lock()
	batch := t.swapBuf()
	t.mu.Unlock()
	if len(batch) > 0 {
		t.flushMu.Lock()
		t.flushFn(batch)
		t.flushMu.Unlock()
	}
}

func (t *Throttle[T]) loop(ctx context.Context) {
	defer close(t.done)
	ticker := time.NewTicker(t.cfg.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.mu.Lock()
			batch := t.swapBuf()
			t.mu.Unlock()
			if len(batch) > 0 {
				t.flushMu.Lock()
				t.flushFn(batch)
				t.flushMu.Unlock()
			}
		}
	}
}

func (t *Throttle[T]) swapBuf() []T {
	old := t.buf
	t.buf = make([]T, 0, t.cfg.maxBatch)
	return old
}
