// Package tally 提供高频累计聚合 + 批量刷写:把海量小额增量在内存里按 key 累加,
// 定时(或攒够阈值)合并成一批交给 flush 回调一次性处理,削平写放大。
//
// 解决的问题:直播间点赞、刷礼物、观看时长、埋点计数——每秒成百上千次小额 +1,
// 逐笔写库/推送会打爆下游。tally 把"高频写"聚合成"低频批量写":
// N 次 Add 只触发少量 flush,每次 flush 拿到的是合并后的 map[key]delta。
//
// 与相邻原语的分工:
//   - wallet:逐笔精确账本(必须不丢、可审计),适合货币;tally 是可聚合的计数
//     (点赞/人气/礼物数),容忍进程崩溃丢失最后一个未 flush 窗口;
//   - counter:窗口内累计用于"读/配额判断",不落地;tally 聚合后"刷写到下游";
//   - scheduler:通用工作池,tally 专注"累加→合并→批量交付"这一模式。
//
// 触发 flush 的两个条件(满足其一):到达 flushInterval、或累积的 key 数达 maxKeys。
// flush 在独立 goroutine 串行执行(不重叠),panic 被 pkg/safe 恢复。
// Stop 会做最后一次 flush(除非 WithFlushOnStop(false)),确保尾部数据不丢。
//
// 泛型 V 为增量的数值类型(如 int64 / float64)。并发安全。零值不可用,用 New 构造。
package tally

import (
	"context"
	"sync"
	"time"

	"github.com/rushteam/beauty/pkg/safe"
)

// Number 约束:可累加的数值类型。
type Number interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 |
		~float32 | ~float64
}

// FlushFunc 批量刷写回调。收到的是自上次 flush 以来合并的增量快照(非空)。
// 回调应尽快返回;其内部错误由回调自行处理(tally 不重试、不回滚累积)。
type FlushFunc[V Number] func(ctx context.Context, batch map[string]V)

// config 配置。
type config struct {
	flushInterval time.Duration
	maxKeys       int
	flushOnStop   bool
}

// Option 配置 Tally。
type Option func(*config)

// WithFlushInterval 设置定时刷写间隔(默认 1s)。
func WithFlushInterval(d time.Duration) Option {
	return func(c *config) { c.flushInterval = d }
}

// WithMaxKeys 设置累积 key 数阈值:不同 key 累积到此数立即触发刷写(默认 0=不按数量触发,仅定时)。
// 用于突发高基数场景及时释放内存。
func WithMaxKeys(n int) Option {
	return func(c *config) { c.maxKeys = n }
}

// WithFlushOnStop 设置 Stop 时是否做最后一次刷写(默认 true)。
func WithFlushOnStop(on bool) Option {
	return func(c *config) { c.flushOnStop = on }
}

// Tally 高频累计聚合器。零值不可用,用 New 构造。并发安全。
type Tally[V Number] struct {
	cfg     config
	flushFn FlushFunc[V]

	mu   sync.Mutex
	buf  map[string]V
	dirt int // 当前累积的不同 key 数(= len(buf),缓存避免频繁取 len)

	flushCh chan struct{}
	stopCh  chan struct{}
	stop    sync.Once
	wg      sync.WaitGroup
}

// New 创建聚合器并启动后台刷写 goroutine。flush 为批量回调(不可为 nil)。
func New[V Number](flush FlushFunc[V], opts ...Option) *Tally[V] {
	cfg := config{flushInterval: time.Second, flushOnStop: true}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.flushInterval <= 0 {
		cfg.flushInterval = time.Second
	}
	t := &Tally[V]{
		cfg:     cfg,
		flushFn: flush,
		buf:     make(map[string]V),
		flushCh: make(chan struct{}, 1),
		stopCh:  make(chan struct{}),
	}
	t.wg.Add(1)
	go t.loop()
	return t
}

// Add 给 key 累加 delta(高频调用路径,只做内存累加)。
// 若累积 key 数达到 maxKeys 阈值,非阻塞地触发一次刷写。
func (t *Tally[V]) Add(key string, delta V) {
	t.mu.Lock()
	if _, ok := t.buf[key]; !ok {
		t.dirt++
	}
	t.buf[key] += delta
	over := t.cfg.maxKeys > 0 && t.dirt >= t.cfg.maxKeys
	t.mu.Unlock()

	if over {
		t.triggerFlush()
	}
}

// Flush 主动触发一次异步刷写(非阻塞)。当前有累积则会尽快 flush。
func (t *Tally[V]) Flush() { t.triggerFlush() }

// Pending 返回当前尚未刷写的不同 key 数(近似,用于观测)。
func (t *Tally[V]) Pending() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.buf)
}

func (t *Tally[V]) triggerFlush() {
	select {
	case t.flushCh <- struct{}{}:
	default:
	}
}

// swap 取出当前累积并重置缓冲,返回待刷写的批(可能为空)。
func (t *Tally[V]) swap() map[string]V {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.buf) == 0 {
		return nil
	}
	batch := t.buf
	t.buf = make(map[string]V)
	t.dirt = 0
	return batch
}

// doFlush 取出并交给回调(panic 恢复)。
func (t *Tally[V]) doFlush(ctx context.Context) {
	batch := t.swap()
	if len(batch) == 0 {
		return
	}
	_ = safe.Run(func() error {
		t.flushFn(ctx, batch)
		return nil
	})
}

// loop 后台刷写循环:定时 + 被触发,串行 flush(不重叠)。
func (t *Tally[V]) loop() {
	defer t.wg.Done()
	ticker := time.NewTicker(t.cfg.flushInterval)
	defer ticker.Stop()
	ctx := context.Background()
	for {
		select {
		case <-ticker.C:
			t.doFlush(ctx)
		case <-t.flushCh:
			t.doFlush(ctx)
		case <-t.stopCh:
			if t.cfg.flushOnStop {
				t.doFlush(ctx) // 尾部刷写,不丢最后一个窗口
			}
			return
		}
	}
}

// Stop 停止后台刷写(默认做最后一次刷写)。幂等,阻塞直到刷写循环退出。
func (t *Tally[V]) Stop() {
	t.stop.Do(func() { close(t.stopCh) })
	t.wg.Wait()
}
