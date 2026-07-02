// Package delayqueue 提供定点单次触发的延迟任务原语:注册一个未来时刻要执行的
// 回调,到点由后台 goroutine 触发;支持按 key 取消 / 改期。
//
// 与相邻原语的分工:
//   - pkg/scheduler 是即时工作池(投递即尽快执行),本包是"定点单次"(delay 后触发);
//   - pkg/service/cron 是周期任务(反复触发),本包是一次性(触发即销毁);
//   - pkg/ephemeral 是 TTL KV(到点删数据),本包是到点跑回调(可视作 ephemeral 的
//     "过期即回调"版)。
//
// 典型场景(实时游戏 / 社交):开局倒计时、buff/静音到期、房间空闲踢人、订单
// 15 分钟未支付取消、匹配 60s 超时兜底、限时活动结束结算。
//
// 实现:最小堆(按触发时刻排序)+ 单 goroutine 定时器驱动,堆顶最近到期时间用
// 一个 time.Timer 等待;Schedule/Cancel 通过唤醒信号让驱动 goroutine 重算等待时长。
// 回调在独立 goroutine 执行(复用 pkg/safe,panic 被恢复),不阻塞驱动循环。
//
// 零值不可用,用 New 构造。并发安全。Stop 后驱动 goroutine 退出,未触发的任务丢弃。
package delayqueue

import (
	"container/heap"
	"sync"
	"time"

	"github.com/rushteam/beauty/pkg/safe"
)

// task 一个待触发任务。
type task struct {
	key    string
	fireAt int64 // unix nano,触发时刻
	fn     func()
	index  int  // 在堆中的下标(heap.Interface 维护)
	dead   bool // 已取消/已触发标记(惰性删除)
}

// taskHeap 按 fireAt 升序的最小堆。
type taskHeap []*task

func (h taskHeap) Len() int           { return len(h) }
func (h taskHeap) Less(i, j int) bool { return h[i].fireAt < h[j].fireAt }
func (h taskHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i]; h[i].index = i; h[j].index = j }
func (h *taskHeap) Push(x any)        { t := x.(*task); t.index = len(*h); *h = append(*h, t) }
func (h *taskHeap) Pop() any {
	old := *h
	n := len(old)
	t := old[n-1]
	old[n-1] = nil
	t.index = -1
	*h = old[:n-1]
	return t
}

// config 配置。
type config struct {
	onPanic func(key string, err error)
}

// Option 配置 Queue。
type Option func(*config)

// WithOnPanic 设置回调 panic 时的钩子。err 为 *safe.PanicError(含 panic 值与堆栈)。
// 不设置时 panic 被 pkg/safe 静默恢复。
func WithOnPanic(fn func(key string, err error)) Option {
	return func(c *config) { c.onPanic = fn }
}

// Queue 延迟任务队列。按 key 维护待触发任务,到点单次触发。
// 零值不可用,用 New 构造。并发安全。
type Queue struct {
	cfg    config
	mu     sync.Mutex
	h      taskHeap
	byKey  map[string]*task // key → 当前有效任务(用于取消/改期)
	wakeCh chan struct{}    // 唤醒驱动 goroutine 重算等待
	stopCh chan struct{}
	stop   sync.Once
}

// New 创建延迟队列并启动驱动 goroutine。
func New(opts ...Option) *Queue {
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}
	q := &Queue{
		cfg:    cfg,
		byKey:  make(map[string]*task),
		wakeCh: make(chan struct{}, 1),
		stopCh: make(chan struct{}),
	}
	heap.Init(&q.h)
	go q.run()
	return q
}

// Schedule 注册 key 在 delay 后触发 fn。同 key 已存在则改期为新的 delay(覆盖旧任务)。
// delay<=0 视为立即触发(下一个驱动循环)。fn 为 nil 时忽略。
// 返回是否覆盖了已存在的同 key 任务。
func (q *Queue) Schedule(key string, delay time.Duration, fn func()) (replaced bool) {
	if fn == nil {
		return false
	}
	fireAt := time.Now().Add(delay).UnixNano()
	q.mu.Lock()
	if old, ok := q.byKey[key]; ok {
		old.dead = true // 惰性删除旧任务
		replaced = true
	}
	t := &task{key: key, fireAt: fireAt, fn: fn}
	q.byKey[key] = t
	heap.Push(&q.h, t)
	q.mu.Unlock()
	q.wake()
	return replaced
}

// Cancel 取消 key 对应的待触发任务。返回是否确有任务被取消(false=不存在或已触发)。
func (q *Queue) Cancel(key string) bool {
	q.mu.Lock()
	t, ok := q.byKey[key]
	if ok {
		t.dead = true
		delete(q.byKey, key)
	}
	q.mu.Unlock()
	if ok {
		q.wake() // 堆顶可能被取消,唤醒重算
	}
	return ok
}

// Len 返回当前待触发任务数(有效任务,不含已取消未清理的)。
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.byKey)
}

// Stop 停止驱动 goroutine。幂等。未触发的任务被丢弃。
func (q *Queue) Stop() {
	q.stop.Do(func() { close(q.stopCh) })
}

// wake 非阻塞地唤醒驱动 goroutine 重算等待时长。
func (q *Queue) wake() {
	select {
	case q.wakeCh <- struct{}{}:
	default:
	}
}

// run 驱动循环:取堆顶最近到期任务,等到点后触发;Schedule/Cancel 通过 wake 打断等待重算。
func (q *Queue) run() {
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()

	for {
		q.mu.Lock()
		// 弹出所有已到期或已取消的堆顶。
		now := time.Now().UnixNano()
		var ready []*task
		var wait time.Duration = -1
		for q.h.Len() > 0 {
			top := q.h[0]
			if top.dead {
				heap.Pop(&q.h)
				continue
			}
			if top.fireAt <= now {
				heap.Pop(&q.h)
				top.dead = true
				delete(q.byKey, top.key)
				ready = append(ready, top)
				continue
			}
			wait = time.Duration(top.fireAt - now)
			break
		}
		q.mu.Unlock()

		// 锁外触发到期回调(独立 goroutine,panic 被恢复,不阻塞驱动)。
		for _, t := range ready {
			safe.Go(t.fn, func(err error) {
				if q.cfg.onPanic != nil {
					q.cfg.onPanic(t.key, err)
				}
			})
		}

		// 计算等待:有下一个任务则等到它到期,否则长睡等唤醒。
		if wait < 0 {
			wait = time.Hour
		}
		timer.Reset(wait)
		select {
		case <-timer.C:
		case <-q.wakeCh:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-q.stopCh:
			return
		}
	}
}
