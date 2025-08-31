package xgo

import (
	"container/list"
	"context"
	"fmt"
	"math"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rushteam/beauty/pkg/logger"
)

// Pool 协程池接口，提供安全的协程执行能力
type Pool interface {
	// Go 提交一个任务到协程池执行
	Go(f func())
	// GoWithContext 提交带上下文的任务
	GoWithContext(ctx context.Context, f func(ctx context.Context))
	// NewWaitGroup 创建一个新的 WaitGroup，用于批量任务管理
	NewWaitGroup() *WaitGroup
	// NewErrorGroup 创建一个新的 ErrorGroup，用于错误收集和快速失败
	NewErrorGroup() *ErrorGroup
	// NewErrorGroupWithContext 创建带上下文的 ErrorGroup
	NewErrorGroupWithContext(ctx context.Context) (*ErrorGroup, context.Context)
	// Workers 返回当前活跃的 worker 数量
	Workers() int32
	// PendingTasks 返回待处理的任务数量
	PendingTasks() int
	// Close 优雅关闭协程池，等待所有任务完成
	Close() error
	// CloseWithTimeout 带超时的关闭协程池
	CloseWithTimeout(timeout time.Duration) error
}

// WaitGroup 协程池的等待组，提供批量任务管理能力
type WaitGroup struct {
	pool *wokerpool
	wg   sync.WaitGroup
}

// Go 提交任务到协程池并加入等待组
func (w *WaitGroup) Go(f func()) {
	if atomic.LoadInt32(&w.pool.closed) == 1 {
		logger.Error("cannot submit task to closed worker pool")
		return
	}

	w.wg.Add(1)
	task := &Task{
		fn: func() {
			defer w.wg.Done()
			f()
		},
	}

	w.pool.addTask(task)
	if (w.pool.taskNum() >= w.pool.scaleThreshold && w.pool.Workers() < atomic.LoadInt32(&w.pool.cap)) || w.pool.Workers() == 0 {
		w.pool.run()
	}
}

// GoWithContext 提交带上下文的任务到协程池并加入等待组
func (w *WaitGroup) GoWithContext(ctx context.Context, f func(ctx context.Context)) {
	if atomic.LoadInt32(&w.pool.closed) == 1 {
		logger.Error("cannot submit task to closed worker pool")
		return
	}

	w.wg.Add(1)
	task := &Task{
		fn: func() {
			defer w.wg.Done()
			f(ctx)
		},
		ctx: ctx,
	}

	w.pool.addTask(task)
	if (w.pool.taskNum() >= w.pool.scaleThreshold && w.pool.Workers() < atomic.LoadInt32(&w.pool.cap)) || w.pool.Workers() == 0 {
		w.pool.run()
	}
}

// Wait 等待所有任务完成
func (w *WaitGroup) Wait() {
	w.wg.Wait()
}

// Add 手动增加等待计数（高级用法）
func (w *WaitGroup) Add(delta int) {
	w.wg.Add(delta)
}

// Done 手动减少等待计数（高级用法）
func (w *WaitGroup) Done() {
	w.wg.Done()
}

// ErrorGroup 协程池的错误组，提供错误收集和快速失败能力
type ErrorGroup struct {
	pool   *wokerpool
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc

	errOnce sync.Once
	err     error
	errMu   sync.RWMutex
}

// Go 提交任务到协程池并加入错误组
func (eg *ErrorGroup) Go(f func() error) {
	if atomic.LoadInt32(&eg.pool.closed) == 1 {
		logger.Error("cannot submit task to closed worker pool")
		return
	}

	eg.wg.Add(1)
	task := &Task{
		fn: func() {
			defer eg.wg.Done()

			if err := f(); err != nil {
				eg.errOnce.Do(func() {
					eg.errMu.Lock()
					eg.err = err
					eg.errMu.Unlock()

					// 取消其他任务
					if eg.cancel != nil {
						eg.cancel()
					}
				})
			}
		},
		ctx: eg.ctx,
	}

	eg.pool.addTask(task)
	if (eg.pool.taskNum() >= eg.pool.scaleThreshold && eg.pool.Workers() < atomic.LoadInt32(&eg.pool.cap)) || eg.pool.Workers() == 0 {
		eg.pool.run()
	}
}

// GoWithContext 提交带上下文的任务到协程池并加入错误组
func (eg *ErrorGroup) GoWithContext(ctx context.Context, f func(ctx context.Context) error) {
	if atomic.LoadInt32(&eg.pool.closed) == 1 {
		logger.Error("cannot submit task to closed worker pool")
		return
	}

	// 合并上下文
	mergedCtx := ctx
	if eg.ctx != nil {
		var cancel context.CancelFunc
		mergedCtx, cancel = context.WithCancel(ctx)

		// 监听父上下文取消
		go func() {
			select {
			case <-eg.ctx.Done():
				cancel()
			case <-ctx.Done():
				cancel()
			}
		}()
	}

	eg.wg.Add(1)
	task := &Task{
		fn: func() {
			defer eg.wg.Done()

			if err := f(mergedCtx); err != nil {
				eg.errOnce.Do(func() {
					eg.errMu.Lock()
					eg.err = err
					eg.errMu.Unlock()

					// 取消其他任务
					if eg.cancel != nil {
						eg.cancel()
					}
				})
			}
		},
		ctx: mergedCtx,
	}

	eg.pool.addTask(task)
	if (eg.pool.taskNum() >= eg.pool.scaleThreshold && eg.pool.Workers() < atomic.LoadInt32(&eg.pool.cap)) || eg.pool.Workers() == 0 {
		eg.pool.run()
	}
}

// Wait 等待所有任务完成并返回第一个错误
func (eg *ErrorGroup) Wait() error {
	eg.wg.Wait()

	eg.errMu.RLock()
	defer eg.errMu.RUnlock()
	return eg.err
}

// Context 返回 ErrorGroup 的上下文
func (eg *ErrorGroup) Context() context.Context {
	return eg.ctx
}

// Task 任务结构体
type Task struct {
	fn   func()
	ctx  context.Context
	name string // 任务名称，用于调试
}

type wokerpool struct {
	cap            int32
	scaleThreshold int
	workerNum      int32
	closed         int32 // 0: 运行中, 1: 已关闭

	taskLock sync.RWMutex
	tasks    *list.List

	panicHandler func(taskName string, panicValue any, stack []byte)

	// 用于优雅关闭
	closeCh chan struct{}
	wg      sync.WaitGroup // 等待所有 worker 完成
}

func WithSetCap(cap int32) Option {
	return func(p *wokerpool) {
		p.cap = cap
	}
}

// WithPanicHandler 设置 panic 处理函数
func WithPanicHandler(f func(taskName string, panicValue any, stack []byte)) Option {
	return func(p *wokerpool) {
		p.panicHandler = f
	}
}

// WithScaleThreshold 设置扩容阈值
func WithScaleThreshold(threshold int) Option {
	return func(p *wokerpool) {
		p.scaleThreshold = threshold
	}
}

type Option func(p *wokerpool)

func New(opts ...Option) Pool {
	p := &wokerpool{
		cap:            math.MaxInt32,
		scaleThreshold: 1,
		tasks:          new(list.List),
		closeCh:        make(chan struct{}),
	}
	// 默认的 panic 处理函数
	p.panicHandler = func(taskName string, panicValue any, stack []byte) {
		if taskName != "" {
			logger.Error("panic in worker pool task [%s]: %v\nstack: %s", taskName, panicValue, string(stack))
		} else {
			logger.Error("panic in worker pool: %v\nstack: %s", panicValue, string(stack))
		}
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

func (p *wokerpool) Go(f func()) {
	if atomic.LoadInt32(&p.closed) == 1 {
		logger.Error("cannot submit task to closed worker pool")
		return
	}

	task := &Task{
		fn: f,
	}

	p.addTask(task)
	if (p.taskNum() >= p.scaleThreshold && p.Workers() < atomic.LoadInt32(&p.cap)) || p.Workers() == 0 {
		p.run()
	}
}

func (p *wokerpool) NewWaitGroup() *WaitGroup {
	return &WaitGroup{
		pool: p,
	}
}

func (p *wokerpool) NewErrorGroup() *ErrorGroup {
	return &ErrorGroup{
		pool: p,
	}
}

func (p *wokerpool) NewErrorGroupWithContext(ctx context.Context) (*ErrorGroup, context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	eg := &ErrorGroup{
		pool:   p,
		ctx:    ctx,
		cancel: cancel,
	}
	return eg, ctx
}

func (p *wokerpool) GoWithContext(ctx context.Context, f func(ctx context.Context)) {
	if atomic.LoadInt32(&p.closed) == 1 {
		logger.Error("cannot submit task to closed worker pool")
		return
	}

	task := &Task{
		fn: func() {
			f(ctx)
		},
		ctx: ctx,
	}

	p.addTask(task)
	if (p.taskNum() >= p.scaleThreshold && p.Workers() < atomic.LoadInt32(&p.cap)) || p.Workers() == 0 {
		p.run()
	}
}

func (p *wokerpool) addTask(task *Task) {
	p.taskLock.Lock()
	defer p.taskLock.Unlock()
	p.tasks.PushBack(task)
}

func (p *wokerpool) taskNum() int {
	p.taskLock.RLock()
	defer p.taskLock.RUnlock()
	return p.tasks.Len()
}

func (p *wokerpool) PendingTasks() int {
	return p.taskNum()
}

func (p *wokerpool) popTask() *Task {
	p.taskLock.Lock()
	defer p.taskLock.Unlock()
	el := p.tasks.Front()
	if el == nil {
		return nil
	}
	p.tasks.Remove(el)
	return el.Value.(*Task)
}

func (p *wokerpool) run() {
	atomic.AddInt32(&p.workerNum, 1)
	p.wg.Add(1) // 为优雅关闭添加计数

	go func() {
		defer func() {
			atomic.AddInt32(&p.workerNum, -1)
			p.wg.Done() // worker 完成时减少计数
		}()

		for {
			select {
			case <-p.closeCh:
				// 收到关闭信号，退出 worker
				return
			default:
				task := p.popTask()
				if task == nil {
					// 没有任务，检查是否应该退出
					if atomic.LoadInt32(&p.closed) == 1 {
						return
					}
					// 暂时没有任务，继续等待
					time.Sleep(time.Millisecond * 10)
					continue
				}

				// 检查任务上下文是否已取消
				if task.ctx != nil {
					select {
					case <-task.ctx.Done():
						// 任务已取消，跳过执行
						continue
					default:
					}
				}

				// 执行任务
				p.executeTask(task)
			}
		}
	}()
}

func (p *wokerpool) executeTask(task *Task) {
	defer func() {
		if r := recover(); r != nil && p.panicHandler != nil {
			stack := debug.Stack()
			p.panicHandler(task.name, r, stack)
		}
	}()

	task.fn()
}

func (p *wokerpool) Workers() int32 {
	return atomic.LoadInt32(&p.workerNum)
}

// Close 优雅关闭协程池，等待所有任务完成
func (p *wokerpool) Close() error {
	return p.CloseWithTimeout(0) // 无超时限制
}

// CloseWithTimeout 带超时的关闭协程池
func (p *wokerpool) CloseWithTimeout(timeout time.Duration) error {
	// 设置关闭标志
	if !atomic.CompareAndSwapInt32(&p.closed, 0, 1) {
		return fmt.Errorf("worker pool already closed")
	}

	// 关闭信号通道，通知所有 worker 退出
	close(p.closeCh)

	if timeout > 0 {
		// 带超时等待
		done := make(chan struct{})
		go func() {
			p.wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			return nil
		case <-time.After(timeout):
			return fmt.Errorf("worker pool close timeout after %v", timeout)
		}
	} else {
		// 无超时等待
		p.wg.Wait()
		return nil
	}
}

// 全局默认协程池
var defaultPool Pool

func init() {
	defaultPool = New(WithSetCap(1000))
}

// SafeGo 使用默认协程池安全执行协程
func SafeGo(f func()) {
	defaultPool.Go(f)
}

// SafeGoWithContext 使用默认协程池安全执行带上下文的协程
func SafeGoWithContext(ctx context.Context, f func(ctx context.Context)) {
	defaultPool.GoWithContext(ctx, f)
}

// NewWaitGroup 使用默认协程池创建 WaitGroup
func NewWaitGroup() *WaitGroup {
	return defaultPool.NewWaitGroup()
}

// NewErrorGroup 使用默认协程池创建 ErrorGroup
func NewErrorGroup() *ErrorGroup {
	return defaultPool.NewErrorGroup()
}

// NewErrorGroupWithContext 使用默认协程池创建带上下文的 ErrorGroup
func NewErrorGroupWithContext(ctx context.Context) (*ErrorGroup, context.Context) {
	return defaultPool.NewErrorGroupWithContext(ctx)
}
