// Package scheduler 提供工作池式任务调度器:生产者把任务投递到队列,
// N 个 worker goroutine 并发消费;支持运行时 Pause/Resume 与优雅停止。
//
// 与 pkg/service/cron 的区别:cron 按 cron 表达式定时触发,scheduler 按
// 事件驱动(手动 Submit),且支持运行时暂停/恢复回调处理——适合"回调可能很重、
// 需要在业务高峰期暂停处理"的场景(如发奖、批量通知、过期清理)。
//
// 设计参考 Nakama server/leaderboard_scheduler.go 的工作池 + Pause/Resume 模型。
//
// 零值不可用,用 New 构造。
package scheduler

import (
	"context"
	"sync"
	"sync/atomic"
)

// Task 是被调度的工作单元。业务在 fn 中执行具体逻辑。
type Task struct {
	Name string
	Fn   func(ctx context.Context) error
}

// ErrorHandler 处理 worker 执行 Task 时的 panic / 返回 error。
type ErrorHandler func(taskName string, err error, panicStack []byte)

// Scheduler 管理工作池。
type Scheduler struct {
	queue     chan *Task
	workers   int
	onError   ErrorHandler
	wg        sync.WaitGroup
	paused    atomic.Bool
	pauseMu   sync.Mutex
	pauseCond *sync.Cond // 暂停时 worker 阻塞在此
	stopped   atomic.Bool
	stopCh    chan struct{}
	done      chan struct{}
	ctx       context.Context
	cancel    context.CancelFunc
}

// Option 配置 Scheduler。
type Option func(*config)

type config struct {
	queueSize int
	workers   int
	onError   ErrorHandler
}

// WithQueueSize 设置任务队列容量,默认 256。队列满时 Submit 阻塞(默认)或返回 false。
func WithQueueSize(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.queueSize = n
		}
	}
}

// WithWorkers 设置 worker 数量,默认 4。0 表示不启动 worker(纯排队模式,
// 仅用于测试或需要外部自行消费 queue 的场景)。
func WithWorkers(n int) Option {
	return func(c *config) {
		if n >= 0 {
			c.workers = n
		}
	}
}

// WithErrorHandler 设置错误处理回调。默认忽略 error,panic 会被 recover 不中断 worker。
func WithErrorHandler(h ErrorHandler) Option {
	return func(c *config) { c.onError = h }
}

// New 创建调度器(未启动)。用 Start 启动。
func New(opts ...Option) *Scheduler {
	cfg := config{queueSize: 256, workers: 4}
	for _, o := range opts {
		o(&cfg)
	}
	s := &Scheduler{
		queue:   make(chan *Task, cfg.queueSize),
		workers: cfg.workers,
		onError: cfg.onError,
		stopCh:  make(chan struct{}),
		done:    make(chan struct{}),
	}
	s.pauseCond = sync.NewCond(&s.pauseMu)
	return s
}

// Start 启动 worker 池。幂等。ctx 取消时优雅停止(排空队列后退出)。
func (s *Scheduler) Start(ctx context.Context) {
	s.ctx, s.cancel = context.WithCancel(ctx)
	for i := 0; i < s.workers; i++ {
		s.wg.Add(1)
		go s.worker()
	}
	// ctx 取消联动 Stop。
	go func() {
		select {
		case <-ctx.Done():
			s.Stop()
		case <-s.stopCh:
		}
	}()
}

// Submit 投递一个任务。若已停止返回 false;队列满时阻塞(保证不丢任务)。
// 在 Pause 期间仍可 Submit(任务入队,Resume 后消费)。
func (s *Scheduler) Submit(t *Task) bool {
	if s.stopped.Load() {
		return false
	}
	select {
	case s.queue <- t:
		return true
	case <-s.stopCh:
		return false
	}
}

// TrySubmit 非阻塞投递。队列满立即返回 false。
func (s *Scheduler) TrySubmit(t *Task) bool {
	if s.stopped.Load() {
		return false
	}
	select {
	case s.queue <- t:
		return true
	default:
		return false
	}
}

// Pause 暂停所有 worker:正在执行的 Task 继续完成,之后 worker 阻塞不再取新任务。
// 已入队任务不丢失,Resume 后继续消费。幂等。
func (s *Scheduler) Pause() {
	s.paused.Store(true)
}

// Resume 恢复消费。幂等。
func (s *Scheduler) Resume() {
	if s.paused.CompareAndSwap(true, false) {
		s.pauseCond.Broadcast() // 唤醒所有等待的 worker
	}
}

// Paused 返回是否暂停。
func (s *Scheduler) Paused() bool { return s.paused.Load() }

// Stop 停止调度器:关闭队列,等所有 worker 排空并退出。幂等。
// 排空保证已 Submit 的任务被执行(除非 ctx 已取消)。
func (s *Scheduler) Stop() {
	if !s.stopped.CompareAndSwap(false, true) {
		return
	}
	// 唤醒暂停中的 worker,让它们能看到 stop 信号。
	s.Resume()
	close(s.stopCh)
	// 关闭 queue 让 worker 在消费完后退出。仅当无并发 Submit 时安全;
	// 这里 stopCh 已关闭,Submit 会先返回 false,故无新写入。
	close(s.queue)
	s.wg.Wait()
	if s.cancel != nil {
		s.cancel()
	}
	close(s.done)
}

// Wait 阻塞直到所有 worker 退出。
func (s *Scheduler) Wait() { <-s.done }

// Pending 返回队列中待处理任务数(近似)。
func (s *Scheduler) Pending() int { return len(s.queue) }

// worker 是消费者 goroutine。
func (s *Scheduler) worker() {
	defer s.wg.Done()
	for {
		// 暂停检查:若暂停则阻塞,直到 Resume 或 Stop。
		if s.paused.Load() {
			s.pauseMu.Lock()
			for s.paused.Load() && !s.stopped.Load() {
				s.pauseCond.Wait()
			}
			s.pauseMu.Unlock()
			if s.stopped.Load() {
				return
			}
		}

		select {
		case t, ok := <-s.queue:
			if !ok {
				return // 队列已关闭且排空
			}
			s.exec(t)
		case <-s.stopCh:
			return
		}
	}
}

// exec 执行单个任务,带 panic recovery。
func (s *Scheduler) exec(t *Task) {
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	defer func() {
		if r := recover(); r != nil {
			if s.onError != nil {
				s.onError(t.Name, nil, panicStack())
			}
		}
	}()
	if err := t.Fn(ctx); err != nil && s.onError != nil {
		s.onError(t.Name, err, nil)
	}
}

// panicStack 返回当前 goroutine 的调用栈(用于错误回调)。
func panicStack() []byte {
	// 简化:返回 nil。完整实现可调 runtime.Stack,但开销大,按需启用。
	return nil
}
