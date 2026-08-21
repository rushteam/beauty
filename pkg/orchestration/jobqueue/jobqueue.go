// Package jobqueue 提供带优先级、进度上报、生命周期事件的任务队列。
//
// 借鉴 BullMQ 的架构分层(Queue / Worker / Events):
//   - Queue: 投递任务,支持优先级(数值越小优先级越高)、延迟投递;
//   - Worker: N 个 goroutine 并发消费,支持 Pause/Resume;
//   - EventHook: 生命周期事件回调(Submit/Start/Progress/Complete/Fail),
//     用于构建可观测性(日志、metrics、dashboard)。
//
// 与 pkg/orchestration/scheduler 的区别:
//   - scheduler 是 FIFO channel + 工作池,不支持优先级/进度/事件;
//   - jobqueue 用 priority 堆做排序,每个 Job 有状态机 + 进度回调。
//
// 与 pkg/orchestration/delayqueue 的区别:
//   - delayqueue 仅"到点触发回调",不关心执行状态与并发控制;
//   - jobqueue 关注"完整的 Job 生命周期":排队→执行→报告进度→完成/失败。
//
// 进程内实现(不持久化);分布式持久化版本见 contrib/redisqueue。
// 零值不可用,用 New 构造。并发安全。
package jobqueue

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rushteam/beauty/pkg/foundation/priority"
)

// JobState 任务状态。
type JobState int

const (
	StateWaiting   JobState = iota // 在队列中等待执行
	StateDelayed                   // 延迟中(到期后转 Waiting)
	StateActive                    // 正在被 worker 执行
	StateCompleted                 // 执行成功
	StateFailed                    // 执行失败
)

func (s JobState) String() string {
	switch s {
	case StateWaiting:
		return "waiting"
	case StateDelayed:
		return "delayed"
	case StateActive:
		return "active"
	case StateCompleted:
		return "completed"
	case StateFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Job 是一个待执行的任务。
type Job struct {
	// ID 任务唯一标识。
	ID string
	// Name 任务名称(用于日志/metrics 分组)。
	Name string
	// Priority 优先级:数值越小越优先(0 最高)。默认 0。
	Priority int
	// Payload 任务数据(业务自定义)。
	Payload any
	// Fn 执行函数。ctx 携带 ProgressReporter,可通过 ReportProgress 上报进度。
	Fn func(ctx context.Context, job *Job) error
	// MaxRetries 最大重试次数(0=不重试)。
	MaxRetries int
	// RetryDelay 重试基础延迟(第 n 次 = delay * 2^n)。
	RetryDelay time.Duration
	// Delay 延迟执行:投递后等 Delay 再进入就绪队列。0=立即就绪。
	Delay time.Duration
	// Timeout 单次执行超时(0=不限)。
	Timeout time.Duration

	// --- 运行时状态(只读) ---

	// State 当前状态。
	State JobState
	// Attempts 已尝试次数。
	Attempts int
	// Progress 当前进度(0~100)。
	Progress float64
	// Err 最近一次执行错误。
	Err error
	// CreatedAt 创建时间。
	CreatedAt time.Time
	// StartedAt 开始执行时间。
	StartedAt time.Time
	// CompletedAt 完成时间。
	CompletedAt time.Time

	readyAt time.Time // 就绪时刻(对延迟任务,readyAt = CreatedAt + Delay)
}

// EventType 事件类型。
type EventType int

const (
	EventSubmit   EventType = iota // 任务入队
	EventStart                     // 开始执行
	EventProgress                  // 进度更新
	EventComplete                  // 执行成功
	EventFail                      // 执行失败(含重试耗尽)
	EventRetry                     // 即将重试
)

func (e EventType) String() string {
	switch e {
	case EventSubmit:
		return "submit"
	case EventStart:
		return "start"
	case EventProgress:
		return "progress"
	case EventComplete:
		return "complete"
	case EventFail:
		return "fail"
	case EventRetry:
		return "retry"
	default:
		return "unknown"
	}
}

// Event 一个生命周期事件。
type Event struct {
	Type EventType
	Job  *Job
	Err  error // 仅 EventFail 时有值
}

// EventHook 事件回调接口。实现者可选择性处理感兴趣的事件。
// 回调在 worker goroutine 中同步调用,应尽量轻量(如写 channel / 递增 metric)。
type EventHook interface {
	OnEvent(event Event)
}

// EventHookFunc 函数适配器。
type EventHookFunc func(Event)

func (f EventHookFunc) OnEvent(e Event) { f(e) }

// progressKey 用于从 ctx 中取 progress reporter。
type progressKeyType struct{}

var progressCtxKey = progressKeyType{}

// ReportProgress 在 Job.Fn 内调用,上报执行进度(0~100)。
// 若 ctx 中无 reporter(非 jobqueue 执行),则静默忽略。
func ReportProgress(ctx context.Context, percent float64) {
	if rp, ok := ctx.Value(progressCtxKey).(*progressReporter); ok {
		rp.report(percent)
	}
}

type progressReporter struct {
	job  *Job
	hook EventHook
}

func (p *progressReporter) report(percent float64) {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	p.job.Progress = percent
	if p.hook != nil {
		p.hook.OnEvent(Event{Type: EventProgress, Job: p.job})
	}
}

// Config 配置。
type Config struct {
	Workers   int       // worker 数量,默认 4
	QueueSize int       // 内部就绪信号缓冲,默认 1024
	Hook      EventHook // 事件钩子(nil=不回调)
	OnPanic   func(job *Job, r any, stack []byte)
}

// Option 配置函数。
type Option func(*Config)

// WithWorkers 设置 worker 并发数。默认 4。
func WithWorkers(n int) Option { return func(c *Config) { c.Workers = n } }

// WithQueueSize 设置就绪信号缓冲大小。默认 1024。
func WithQueueSize(n int) Option { return func(c *Config) { c.QueueSize = n } }

// WithHook 设置事件钩子。
func WithHook(h EventHook) Option { return func(c *Config) { c.Hook = h } }

// WithHookFunc 用函数设置事件钩子。
func WithHookFunc(fn func(Event)) Option { return func(c *Config) { c.Hook = EventHookFunc(fn) } }

// WithPanicHandler 设置 panic 处理。
func WithPanicHandler(fn func(job *Job, r any, stack []byte)) Option {
	return func(c *Config) { c.OnPanic = fn }
}

// Queue 带优先级的任务队列。
type Queue struct {
	cfg Config

	mu      sync.Mutex
	pq      *priority.Queue[*Job] // 就绪任务的优先级堆
	delayed []*Job                // 延迟任务列表(由 ticker 驱动转就绪)
	byID    map[string]*Job       // ID → Job(用于查询/取消)
	signal  chan struct{}         // 通知 worker 有新任务就绪

	paused    atomic.Bool
	pauseMu   sync.Mutex
	pauseCond *sync.Cond

	stopped atomic.Bool
	stopCh  chan struct{}
	wg      sync.WaitGroup
	done    chan struct{}

	startOnce sync.Once
	ctx       context.Context
	cancel    context.CancelFunc
}

// New 创建任务队列(未启动)。用 Start 启动 worker。
func New(opts ...Option) *Queue {
	cfg := Config{Workers: 4, QueueSize: 1024}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.Workers < 1 {
		cfg.Workers = 1
	}
	q := &Queue{
		cfg:    cfg,
		pq:     priority.New[*Job](func(a, b *Job) bool { return a.Priority < b.Priority }),
		byID:   make(map[string]*Job),
		signal: make(chan struct{}, cfg.QueueSize),
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
	}
	q.pauseCond = sync.NewCond(&q.pauseMu)
	return q
}

// Start 启动 worker 池和延迟任务调度器。ctx 取消时优雅停止。满足 beauty.Service。
func (q *Queue) Start(ctx context.Context) error {
	q.startOnce.Do(func() {
		q.ctx, q.cancel = context.WithCancel(ctx)
		for i := 0; i < q.cfg.Workers; i++ {
			q.wg.Add(1)
			go q.worker()
		}
		q.wg.Add(1)
		go q.delayScheduler()
		go func() {
			select {
			case <-ctx.Done():
				q.Stop()
			case <-q.stopCh:
			}
		}()
	})
	<-q.done
	return nil
}

// String 满足 beauty.Service。
func (q *Queue) String() string {
	return fmt.Sprintf("jobqueue(workers=%d)", q.cfg.Workers)
}

// Submit 投递一个任务。返回 false 表示队列已停止。
func (q *Queue) Submit(job *Job) bool {
	if q.stopped.Load() {
		return false
	}
	now := time.Now()
	job.CreatedAt = now
	job.State = StateWaiting

	if job.Delay > 0 {
		job.State = StateDelayed
		job.readyAt = now.Add(job.Delay)
		q.mu.Lock()
		q.delayed = append(q.delayed, job)
		if job.ID != "" {
			q.byID[job.ID] = job
		}
		q.mu.Unlock()
	} else {
		job.readyAt = now
		q.enqueue(job)
	}

	q.emit(Event{Type: EventSubmit, Job: job})
	return true
}

// Cancel 取消指定 ID 的任务(仅 Waiting/Delayed 状态可取消)。
func (q *Queue) Cancel(id string) bool {
	q.mu.Lock()
	job, ok := q.byID[id]
	if ok && (job.State == StateWaiting || job.State == StateDelayed) {
		delete(q.byID, id)
		job.State = StateFailed
		job.Err = context.Canceled
		q.mu.Unlock()
		return true
	}
	q.mu.Unlock()
	return false
}

// Pause 暂停消费(不影响投递)。
func (q *Queue) Pause() { q.paused.Store(true) }

// Resume 恢复消费。
func (q *Queue) Resume() {
	if q.paused.CompareAndSwap(true, false) {
		q.pauseCond.Broadcast()
	}
}

// Paused 是否暂停。
func (q *Queue) Paused() bool { return q.paused.Load() }

// Pending 返回等待中的任务数(近似)。
func (q *Queue) Pending() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.pq.Len()
}

// Stop 停止队列。
func (q *Queue) Stop() {
	if !q.stopped.CompareAndSwap(false, true) {
		return
	}
	q.Resume()
	close(q.stopCh)
	q.wg.Wait()
	if q.cancel != nil {
		q.cancel()
	}
	close(q.done)
}

func (q *Queue) enqueue(job *Job) {
	q.mu.Lock()
	job.State = StateWaiting
	q.pq.Push(job)
	if job.ID != "" {
		q.byID[job.ID] = job
	}
	q.mu.Unlock()
	select {
	case q.signal <- struct{}{}:
	default:
	}
}

func (q *Queue) dequeue() (*Job, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for q.pq.Len() > 0 {
		job := q.pq.Pop()
		if job.State != StateWaiting {
			continue // 已取消
		}
		job.State = StateActive
		return job, true
	}
	return nil, false
}

func (q *Queue) worker() {
	defer q.wg.Done()
	for {
		q.waitIfPaused()
		if q.stopped.Load() {
			return
		}

		select {
		case <-q.signal:
			q.waitIfPaused()
			if q.stopped.Load() {
				return
			}
			job, ok := q.dequeue()
			if !ok {
				continue
			}
			q.execute(job)
		case <-q.stopCh:
			return
		}
	}
}

func (q *Queue) waitIfPaused() {
	if q.paused.Load() {
		q.pauseMu.Lock()
		for q.paused.Load() && !q.stopped.Load() {
			q.pauseCond.Wait()
		}
		q.pauseMu.Unlock()
	}
}

func (q *Queue) execute(job *Job) {
	job.Attempts++
	job.StartedAt = time.Now()
	q.emit(Event{Type: EventStart, Job: job})

	rp := &progressReporter{job: job, hook: q.cfg.Hook}
	ctx := context.WithValue(q.ctx, progressCtxKey, rp)
	if job.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, job.Timeout)
		defer cancel()
	}

	err := q.safeExec(ctx, job)

	if err != nil {
		job.Err = err
		if job.Attempts <= job.MaxRetries {
			q.emit(Event{Type: EventRetry, Job: job, Err: err})
			delay := job.RetryDelay * time.Duration(1<<(job.Attempts-1))
			if delay <= 0 {
				delay = 100 * time.Millisecond
			}
			job.State = StateDelayed
			job.readyAt = time.Now().Add(delay)
			q.mu.Lock()
			q.delayed = append(q.delayed, job)
			q.mu.Unlock()
			return
		}
		job.State = StateFailed
		job.CompletedAt = time.Now()
		q.emit(Event{Type: EventFail, Job: job, Err: err})
	} else {
		job.State = StateCompleted
		job.Progress = 100
		job.CompletedAt = time.Now()
		q.emit(Event{Type: EventComplete, Job: job})
	}

	if job.ID != "" {
		q.mu.Lock()
		delete(q.byID, job.ID)
		q.mu.Unlock()
	}
}

func (q *Queue) safeExec(ctx context.Context, job *Job) (err error) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			err = fmt.Errorf("jobqueue: panic in job %q: %v", job.Name, r)
			if q.cfg.OnPanic != nil {
				q.cfg.OnPanic(job, r, stack)
			}
		}
	}()
	return job.Fn(ctx, job)
}

func (q *Queue) delayScheduler() {
	defer q.wg.Done()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case now := <-ticker.C:
			q.mu.Lock()
			var remaining []*Job
			for _, job := range q.delayed {
				if job.State == StateFailed || job.State == StateCompleted {
					continue // 已取消
				}
				if now.After(job.readyAt) || now.Equal(job.readyAt) {
					job.State = StateWaiting
					q.pq.Push(job)
					select {
					case q.signal <- struct{}{}:
					default:
					}
				} else {
					remaining = append(remaining, job)
				}
			}
			q.delayed = remaining
			q.mu.Unlock()
		case <-q.stopCh:
			return
		}
	}
}

func (q *Queue) emit(e Event) {
	if q.cfg.Hook != nil {
		q.cfg.Hook.OnEvent(e)
	}
}
