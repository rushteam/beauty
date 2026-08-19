// Package timerqueue 提供基于最小堆的延时任务队列,由单协程驱动,适用于海量
// 倒计时场景(如 SLG 建筑升级、科技研究、造兵、Buff 过期等)。
//
// 与 pkg/service/cron 的区别:cron 按 cron 表达式周期触发(少量定时),
// timerqueue 按绝对到期时间调度(万级一次性倒计时),内部用最小堆而非每任务
// 一个 goroutine/timer,内存与 runtime 开销极低。
//
// 与 pkg/scheduler 的区别:scheduler 是事件驱动的工作池(Submit 即消费),
// timerqueue 则在指定时刻到期后才执行回调——"延时"是核心语义。
//
// 设计:单 goroutine 轮询最小堆堆顶,到期即弹出执行;添加/取消任务通过
// channel 串行提交,无锁竞争。回调默认在独立 goroutine 中异步执行,不阻塞
// 时间轴。精度由 WithResolution 控制(默认 100ms)。
//
// 实现 beauty.Service(Start/String)+ ReadyNotifier,可直接
// beauty.WithService(queue) 挂进框架,随 app 优雅停机。
//
// 零值不可用,用 New 构造。
package timerqueue

import (
	"container/heap"
	"context"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

// Task 是一个延时任务。
type Task struct {
	// ID 任务唯一标识,用于取消。
	ID string

	// ExecuteAt 绝对到期时刻。
	ExecuteAt time.Time

	// Fn 到期后执行的回调。默认在独立 goroutine 中异步调用(WithSyncCallback
	// 可改为同步,但会阻塞时间轴——仅当回调极轻量时使用)。
	Fn func()

	index int // heap 内部索引
}

// taskHeap 最小堆,按 ExecuteAt 升序。
type taskHeap []*Task

func (h taskHeap) Len() int           { return len(h) }
func (h taskHeap) Less(i, j int) bool { return h[i].ExecuteAt.Before(h[j].ExecuteAt) }
func (h taskHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *taskHeap) Push(x any) {
	t := x.(*Task)
	t.index = len(*h)
	*h = append(*h, t)
}

func (h *taskHeap) Pop() any {
	old := *h
	n := len(old)
	t := old[n-1]
	old[n-1] = nil
	t.index = -1
	*h = old[:n-1]
	return t
}

// command 是通过 channel 串行提交给主循环的操作。
type command struct {
	add    *Task  // 非 nil 表示添加
	cancel string // 非空表示取消(按 ID)
}

// Queue 是最小堆延时任务队列。零值不可用,用 New 构造。
type Queue struct {
	name       string
	resolution time.Duration
	chanSize   int
	syncCb     bool
	onPanic    func(taskID string, r any, stack []byte)

	cmdCh     chan command
	ready     chan struct{}
	readyOnce sync.Once
	pending   atomic.Int64
}

// Option 配置 Queue。
type Option func(*Queue)

// WithName 设置队列名(日志/String 标识)。
func WithName(name string) Option {
	return func(q *Queue) {
		if name != "" {
			q.name = name
		}
	}
}

// WithResolution 设置轮询精度(堆顶检查间隔)。默认 100ms。
// 越小越精确但 CPU 开销越高;SLG 场景 100~200ms 足够。
func WithResolution(d time.Duration) Option {
	return func(q *Queue) {
		if d > 0 {
			q.resolution = d
		}
	}
}

// WithChannelSize 设置命令 channel 容量(添加/取消任务的缓冲)。默认 1024。
func WithChannelSize(n int) Option {
	return func(q *Queue) {
		if n > 0 {
			q.chanSize = n
		}
	}
}

// WithSyncCallback 设置回调同步执行(在主循环 goroutine 内)。
// 仅当回调极轻量(如写 channel、设标志)时使用;否则会阻塞后续到期任务的检查。
// 默认 false:回调在独立 goroutine 中异步执行。
func WithSyncCallback(sync bool) Option {
	return func(q *Queue) { q.syncCb = sync }
}

// WithPanicHandler 设置回调 panic 时的恢复处理。默认仅 slog.Error。
func WithPanicHandler(fn func(taskID string, r any, stack []byte)) Option {
	return func(q *Queue) { q.onPanic = fn }
}

// New 创建延时任务队列(未启动)。用 Start 启动。
func New(opts ...Option) *Queue {
	q := &Queue{
		name:       "timerqueue",
		resolution: 100 * time.Millisecond,
		chanSize:   1024,
	}
	for _, o := range opts {
		o(q)
	}
	q.cmdCh = make(chan command, q.chanSize)
	q.ready = make(chan struct{})
	return q
}

// Add 添加一个延时任务。delay 为相对当前的延迟时长;fn 为到期回调。
// 返回任务 ID(可用于 Cancel)。id 为空时不可取消(但仍会到期执行)。
// 队列未启动或已停止时返回 false。
func (q *Queue) Add(id string, delay time.Duration, fn func()) bool {
	return q.AddAt(id, time.Now().Add(delay), fn)
}

// AddAt 添加一个在绝对时刻 executeAt 到期的任务。
func (q *Queue) AddAt(id string, executeAt time.Time, fn func()) bool {
	if fn == nil {
		return false
	}
	select {
	case q.cmdCh <- command{add: &Task{ID: id, ExecuteAt: executeAt, Fn: fn}}:
		return true
	default:
		return false
	}
}

// Cancel 取消指定 ID 的任务。尚未到期的任务被移除;已到期/已执行的无效果。
// 返回 false 表示命令 channel 已满或队列已停止。
func (q *Queue) Cancel(id string) bool {
	if id == "" {
		return false
	}
	select {
	case q.cmdCh <- command{cancel: id}:
		return true
	default:
		return false
	}
}

// Pending 返回队列中待执行的任务数(近似)。
func (q *Queue) Pending() int64 { return q.pending.Load() }

// Start 启动主循环,直到 ctx 取消——满足 beauty.Service。
func (q *Queue) Start(ctx context.Context) error {
	q.readyOnce.Do(func() { close(q.ready) })

	h := make(taskHeap, 0)
	heap.Init(&h)
	idIndex := make(map[string]*Task) // ID → heap 中的 Task(用于 O(log n) 取消)

	ticker := time.NewTicker(q.resolution)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			q.pending.Store(0)
			return nil

		case cmd := <-q.cmdCh:
			if cmd.add != nil {
				t := cmd.add
				if t.ID != "" {
					if old, ok := idIndex[t.ID]; ok {
						heap.Remove(&h, old.index)
						delete(idIndex, t.ID)
					}
					idIndex[t.ID] = t
				}
				heap.Push(&h, t)
				q.pending.Store(int64(h.Len()))
			}
			if cmd.cancel != "" {
				if t, ok := idIndex[cmd.cancel]; ok {
					heap.Remove(&h, t.index)
					delete(idIndex, cmd.cancel)
					q.pending.Store(int64(h.Len()))
				}
			}

		case now := <-ticker.C:
			for h.Len() > 0 {
				top := h[0]
				if now.Before(top.ExecuteAt) {
					break
				}
				t := heap.Pop(&h).(*Task)
				if t.ID != "" {
					delete(idIndex, t.ID)
				}
				q.fire(t)
			}
			q.pending.Store(int64(h.Len()))
		}
	}
}

// fire 触发到期任务的回调。
func (q *Queue) fire(t *Task) {
	if q.syncCb {
		q.safeCall(t)
		return
	}
	go q.safeCall(t)
}

// safeCall 带 panic recovery 地执行回调。
func (q *Queue) safeCall(t *Task) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			if q.onPanic != nil {
				q.onPanic(t.ID, r, stack)
			} else {
				slog.Error("timerqueue: callback panic",
					"task", t.ID, "recover", r, "stack", string(stack))
			}
		}
	}()
	t.Fn()
}

// Ready 在主循环启动后关闭——满足 beauty.ReadyNotifier。
func (q *Queue) Ready() <-chan struct{} { return q.ready }

// String 满足 beauty.Service。
func (q *Queue) String() string { return "timerqueue.Queue(" + q.name + ")" }
