// Package idempotency 提供幂等执行原语:同一 key 的重复请求只执行一次,
// 后续请求(含并发)复用首次结果,直到该 key 过期。
//
// 解决的问题:网络重试 / 客户端重发 / 消息重投会让同一逻辑操作执行多次——
// 抽卡重复扣钱、充值重复到账、发奖重复发放。给每次操作分配一个幂等 key
// (订单号 / 请求 ID / txID),用本原语包住执行体,重复 key 不再重复执行。
//
// 两条语义合一:
//   - 去重(dedup):key 已有完成结果 → 直接返回缓存的结果,不再执行 fn;
//   - 并发合并(singleflight):同一 key 多个请求同时到达 → 只有一个执行 fn,
//     其余阻塞等待同一结果,避免"缓存击穿式"重复执行。
//
// 结果按 TTL 过期(默认 10 分钟),到点后同 key 可重新执行。fn 返回 error 时
// 默认不缓存(允许重试);可用 WithCacheErrors 改为连错误一起缓存(适合
// "确定性失败无需重试"的场景)。
//
// 泛型 T 为结果类型。并发安全。零值不可用,用 New 构造;Stop 后清扫 goroutine 退出。
package idempotency

import (
	"sync"
	"time"
)

// entry 一条幂等记录。done 关闭后 result/err 才可读(happens-before 由 channel 保证)。
type entry[T any] struct {
	done   chan struct{} // 执行完成信号,关闭后 result/err 可读
	result T
	err    error
	expiry int64 // unix nano,到期点(执行完成后才设定)
}

// config 配置。
type config struct {
	ttl         time.Duration
	cacheErrors bool
	gcInterval  time.Duration
}

// Option 配置 Store。
type Option func(*config)

// WithTTL 设置结果缓存时长(默认 10 分钟)。到期后同 key 可重新执行。
func WithTTL(d time.Duration) Option { return func(c *config) { c.ttl = d } }

// WithCacheErrors 设置是否缓存失败结果(默认 false)。
// false:fn 返回 error 不缓存,同 key 下次请求会重新执行(适合瞬时错误可重试);
// true:错误也按 TTL 缓存,同 key 直接返回该错误(适合确定性失败,避免无谓重试)。
func WithCacheErrors(cache bool) Option { return func(c *config) { c.cacheErrors = cache } }

// WithGCInterval 设置过期清扫间隔(默认 1 分钟)。
func WithGCInterval(d time.Duration) Option { return func(c *config) { c.gcInterval = d } }

// Store 幂等结果存储。按 key 维护"执行中/已完成"记录。
// 零值不可用,用 New 构造。并发安全。
type Store[T any] struct {
	cfg    config
	mu     sync.Mutex
	items  map[string]*entry[T]
	stopCh chan struct{}
	stop   sync.Once
}

// New 创建幂等 Store 并启动清扫 goroutine。
func New[T any](opts ...Option) *Store[T] {
	cfg := config{ttl: 10 * time.Minute, gcInterval: time.Minute}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.ttl <= 0 {
		cfg.ttl = 10 * time.Minute
	}
	if cfg.gcInterval <= 0 {
		cfg.gcInterval = time.Minute
	}
	s := &Store[T]{
		cfg:    cfg,
		items:  make(map[string]*entry[T]),
		stopCh: make(chan struct{}),
	}
	go s.gc()
	return s
}

// Do 以 key 为幂等键执行 fn:
//   - key 首次出现:执行 fn,缓存结果(按配置决定是否缓存 error),返回 (result, err, false);
//   - key 执行中(并发):阻塞等待首次执行完成,返回其结果 + shared=true;
//   - key 已完成且未过期:直接返回缓存结果 + shared=true,不执行 fn。
//
// shared 表示本次结果是否复用自其他请求(true=未真正执行 fn)。
// fn 内 panic 不被捕获——调用方若需防护请在 fn 内自行 recover;panic 会
// 导致等待同 key 的其他请求也观察到该记录被清理(可重试)。
func (s *Store[T]) Do(key string, fn func() (T, error)) (result T, err error, shared bool) {
	now := time.Now().UnixNano()
	s.mu.Lock()
	if e, ok := s.items[key]; ok {
		select {
		case <-e.done:
			// 已完成:未过期则复用;已过期则删除并走新执行。
			if e.expiry == 0 || now < e.expiry {
				s.mu.Unlock()
				return e.result, e.err, true
			}
			delete(s.items, key)
		default:
			// 执行中:等待首次执行完成(锁外等待,避免阻塞其他 key)。
			s.mu.Unlock()
			<-e.done
			return e.result, e.err, true
		}
	}
	// 首次执行:占位并释放锁,让并发同 key 走 "执行中" 分支等待。
	e := &entry[T]{done: make(chan struct{})}
	s.items[key] = e
	s.mu.Unlock()

	// 执行 fn。panic 时清理占位记录,让后续请求可重试,再向上抛出。
	panicked := true
	defer func() {
		if panicked {
			s.mu.Lock()
			// 仅当仍是本记录时删除(避免误删过期后重建的新记录)。
			if cur, ok := s.items[key]; ok && cur == e {
				delete(s.items, key)
			}
			s.mu.Unlock()
			close(e.done) // 唤醒等待者(它们会读到零值 + nil err;panic 场景本就异常)
		}
	}()
	result, err = fn()
	panicked = false

	// 写入结果。error 且未开启缓存错误 → 不缓存,删除占位允许重试。
	if err != nil && !s.cfg.cacheErrors {
		s.mu.Lock()
		if cur, ok := s.items[key]; ok && cur == e {
			delete(s.items, key)
		}
		s.mu.Unlock()
		e.result, e.err = result, err
		close(e.done)
		return result, err, false
	}

	e.result, e.err = result, err
	e.expiry = time.Now().Add(s.cfg.ttl).UnixNano()
	close(e.done)
	return result, err, false
}

// Get 查询 key 的已完成结果(不触发执行)。
// 返回 (result, ok):ok=false 表示无记录、执行中、或已过期。
func (s *Store[T]) Get(key string) (T, bool) {
	var zero T
	s.mu.Lock()
	e, ok := s.items[key]
	s.mu.Unlock()
	if !ok {
		return zero, false
	}
	select {
	case <-e.done:
		if e.expiry != 0 && time.Now().UnixNano() >= e.expiry {
			return zero, false
		}
		return e.result, true
	default:
		return zero, false // 执行中
	}
}

// Forget 立即删除 key 的记录(不论执行中还是已完成)。
// 执行中的记录被 Forget 后,正在执行的 fn 结果不再被后续请求复用。
func (s *Store[T]) Forget(key string) {
	s.mu.Lock()
	delete(s.items, key)
	s.mu.Unlock()
}

// Len 返回当前记录数(含执行中与未清扫的过期记录)。
func (s *Store[T]) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.items)
}

// Stop 停止清扫 goroutine。幂等。
func (s *Store[T]) Stop() {
	s.stop.Do(func() { close(s.stopCh) })
}

// gc 周期清理已过期(且已完成)的记录。执行中的记录(expiry==0)不清。
func (s *Store[T]) gc() {
	ticker := time.NewTicker(s.cfg.gcInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now().UnixNano()
			s.mu.Lock()
			for k, e := range s.items {
				// 只清已完成的记录:done 关闭建立对 expiry 写入的 happens-before,
				// 避免与 Do 中锁外写 expiry 竞争;执行中的记录(done 未关)跳过。
				select {
				case <-e.done:
					if e.expiry != 0 && now >= e.expiry {
						delete(s.items, k)
					}
				default:
				}
			}
			s.mu.Unlock()
		case <-s.stopCh:
			return
		}
	}
}
