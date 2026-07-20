package syncx

import (
	"sync"
	"time"
)

// Batcher 把逐条 Add 的元素攒成批,达到 maxSize 条或距上批首条满 maxWait 时,调用 flush 处理一批。
// 适合批量写库 / 批量推送 / 批量调用。单后台 goroutine 收集,Add 并发安全。零值不可用,用 NewBatcher 构造。
type Batcher[T any] struct {
	in        chan T
	flush     func([]T)
	maxSize   int
	maxWait   time.Duration
	quit      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
}

// NewBatcher 创建批处理器。maxSize>0 为批容量上限,maxWait>0 为一批的最大等待时长(到点即使不满也 flush)。
// flush 在后台 goroutine 串行调用,收到的 slice 仅在本次调用内有效(下一批会复用底层数组前不会重叠,
// 但调用方若需长期持有请自行拷贝)。
func NewBatcher[T any](maxSize int, maxWait time.Duration, flush func([]T)) *Batcher[T] {
	if maxSize <= 0 {
		maxSize = 1
	}
	b := &Batcher[T]{
		in:      make(chan T, maxSize),
		flush:   flush,
		maxSize: maxSize,
		maxWait: maxWait,
		quit:    make(chan struct{}),
	}
	b.wg.Add(1)
	go b.run()
	return b
}

// Add 加入一条元素。Close 之后调用会被丢弃(不 panic)。
func (b *Batcher[T]) Add(item T) {
	select {
	case b.in <- item:
	case <-b.quit:
	}
}

// Close 停止批处理器,flush 掉剩余元素后返回。幂等。
func (b *Batcher[T]) Close() {
	b.closeOnce.Do(func() { close(b.quit) })
	b.wg.Wait()
}

func (b *Batcher[T]) run() {
	defer b.wg.Done()
	buf := make([]T, 0, b.maxSize)
	timer := time.NewTimer(b.maxWait)
	if !timer.Stop() {
		<-timer.C
	}
	timerOn := false

	doFlush := func() {
		if len(buf) > 0 {
			b.flush(buf)
			buf = make([]T, 0, b.maxSize)
		}
		if timerOn {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timerOn = false
		}
	}

	for {
		select {
		case item := <-b.in:
			buf = append(buf, item)
			if len(buf) == 1 && b.maxWait > 0 { // 一批的首条:启动计时
				timer.Reset(b.maxWait)
				timerOn = true
			}
			if len(buf) >= b.maxSize {
				doFlush()
			}
		case <-timer.C:
			timerOn = false
			doFlush()
		case <-b.quit:
			// 排干 channel 里剩余的,连同 buf 一起 flush。
			for {
				select {
				case item := <-b.in:
					buf = append(buf, item)
				default:
					doFlush()
					return
				}
			}
		}
	}
}
