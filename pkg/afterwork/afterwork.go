// Package afterwork 提供请求级后台任务延寿(waitUntil 语义)。
//
// 响应可以立即返回,但被 waitUntil 注册的后台任务会继续跑完——
// 运行时不会在响应后立刻杀掉它。这是"请求级后台任务延寿"。
//
// 与 pkg/safe.Go 的区别:safe.Go 是全局 fire-and-forget,无生命周期绑定;
// afterwork 把任务绑定到请求 ctx,响应返回后由框架调用 Wait() 等待
// 全部延寿任务跑完(带上限,超时则放弃),常用于"响应后副作用":
//   - 响应后发邮件 / 写审计日志;
//   - 响应后触发 webhook / 消息推送;
//   - 响应后异步更新统计指标。
//
// 用法:
//
//	func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
//	    // ... 处理请求 ...
//	    afterwork.Defer(r.Context(), func(ctx context.Context) {
//	        _ = h.webhook.Notify(ctx, event)
//	    })
//	    w.Write(resp) // 响应立即返回,webhook 在响应后继续跑完
//	}
//
// 框架在 handler 返回后调用 afterwork.Wait(ctx) 等待延寿任务。
// 任务 panic 不会崩进程(复用 pkg/safe),panic 转为 error 记录。
//
// 零值不可用,用 WithRegistry 装入 ctx,或用 New 创建独立 Registry。
package afterwork

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/rushteam/beauty/pkg/ctxkey"
	"github.com/rushteam/beauty/pkg/safe"
)

// drainTimeout 是 Wait 默认的最大等待时长:响应返回后给后台任务的上限。
// 超过则放弃剩余任务,避免单个慢任务拖垮关停。可用 WithDrainTimeout 覆盖。
const defaultDrainTimeout = 10 * time.Second

// registryKey 用于把 Registry 注入 ctx。
var registryKey = ctxkey.New[*Registry]()

// Registry 一个请求级延寿任务注册表。
// 一个 Registry 对应一次请求:Defer 投递任务,Wait 等待全部完成。
// 并发安全;Wait 幂等(可重复调用,第二次起立即返回)。
type Registry struct {
	wg      sync.WaitGroup
	mu      sync.Mutex
	tasks   int           // 已投递且未完成数
	done    chan struct{} // 在 Wait 返回后关闭,供幂等复用
	stopped bool          // 是否已 Stop
	drain   time.Duration // Wait 的最大等待时长
	onPanic func(error)   // 任务 panic 回调(可空)
}

// Option 配置 Registry。
type Option func(*config)

type config struct {
	drain   time.Duration
	onPanic func(error)
}

// WithDrainTimeout 设置 Wait 的最大等待时长。<=0 表示无上限(慎用)。
func WithDrainTimeout(d time.Duration) Option {
	return func(c *config) { c.drain = d }
}

// WithPanicHandler 设置任务 panic 回调。默认忽略(panic 已被 safe.Go 恢复)。
func WithPanicHandler(fn func(error)) Option {
	return func(c *config) { c.onPanic = fn }
}

// New 创建一个独立 Registry。
// 通常不需要手动创建——用 WithRegistry 把全局 Registry 装入 ctx,
// 再用 FromContext 在 handler 内取出投递任务。New 用于测试或非 HTTP 场景。
func New(opts ...Option) *Registry {
	cfg := config{drain: defaultDrainTimeout}
	for _, o := range opts {
		o(&cfg)
	}
	return &Registry{
		drain:   cfg.drain,
		onPanic: cfg.onPanic,
		done:    make(chan struct{}),
	}
}

// WithRegistry 把 reg 装入 ctx,返回新 ctx。
// 通常在中间件里包裹每个请求:ctx = afterwork.WithRegistry(ctx, reg),
// 处理完后 afterwork.Wait(ctx) 等待延寿任务。
func WithRegistry(ctx context.Context, reg *Registry) context.Context {
	return ctxkey.With(ctx, registryKey, reg)
}

// FromContext 从 ctx 取出 Registry。没有则返回 nil(此时 Defer 为空操作)。
func FromContext(ctx context.Context) *Registry {
	return ctxkey.MustGet(ctx, registryKey)
}

// Defer 投递一个延寿任务到 ctx 关联的 Registry。
// 任务在 Wait 时被等待(不阻塞当前 handler);响应可立即返回。
// 若 ctx 没有 Registry,任务被丢弃(不 panic)。
// 任务接收派生自 ctx 的子 ctx:请求 ctx 取消时任务应主动退出。
func Defer(ctx context.Context, fn func(ctx context.Context)) {
	reg := FromContext(ctx)
	if reg == nil || fn == nil {
		return
	}
	reg.Defer(ctx, fn)
}

// Defer 把任务投递到本 Registry(无论 ctx 是否有 Registry)。
// 任务 panic 被恢复,转交 WithPanicHandler 回调。
func (r *Registry) Defer(ctx context.Context, fn func(ctx context.Context)) {
	if r == nil || fn == nil {
		return
	}
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return
	}
	r.tasks++
	r.mu.Unlock()
	r.wg.Add(1)
	taskCtx := context.WithoutCancel(ctx) // 响应后任务不应被请求取消立即杀死
	safe.Go(func() {
		defer func() {
			r.mu.Lock()
			r.tasks--
			r.mu.Unlock()
			r.wg.Done()
		}()
		fn(taskCtx)
	}, r.onPanic)
}

// Wait 阻塞直到所有已投递的延寿任务完成,或到达 drain timeout。
// 幂等:可重复调用,第二次起立即返回。
// 在 HTTP 场景下由框架/中间件在 handler 返回后调用。
func (r *Registry) Wait() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.stopped { // 已 Wait 过
		r.mu.Unlock()
		<-r.done
		return
	}
	r.stopped = true
	drain := r.drain
	r.mu.Unlock()

	done := make(chan struct{})
	go func() { r.wg.Wait(); close(done) }()
	if drain <= 0 {
		<-done
	} else {
		select {
		case <-done:
		case <-time.After(drain):
		}
	}
	close(r.done)
}

// Stop 等同于 Wait,提供与 beauty 其他包(Stop 命名惯例)一致的退出语义。
func (r *Registry) Stop() { r.Wait() }

// Pending 返回当前已投递但未完成的任务数(诊断用)。
func (r *Registry) Pending() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tasks
}

// Middleware 返回一个 http 中间件:为每个请求创建独立 Registry 并装入 ctx,
// 调用下游 handler 后调用 Wait() 等待延寿任务跑完(带 drain timeout)。
//
// 这就是 waitUntil 语义在 beauty 的接入点:handler 里 afterwork.Defer(...)
// 投递的响应后副作用,在响应返回后由本中间件统一等待完成。
//
//	h := afterwork.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
//	    // ... 处理 ...
//	    afterwork.Defer(r.Context(), func(ctx context.Context) {
//	        _ = webhook.Notify(ctx, event)
//	    })
//	    w.Write([]byte("ok")) // 响应立即返回
//	    // ← 本中间件在此之后调用 reg.Wait(),webhook 跑完才放行下一个环节
//	}))
//
// 注意:本中间件在 handler 返回后才 Wait,因此 handler 写完响应体后
// 不能再持有 w/r。延寿任务通过 Defer 接收的 taskCtx 工作,不应触碰 w。
func Middleware(opts ...Option) func(http.Handler) http.Handler {
	cfg := config{drain: defaultDrainTimeout}
	for _, o := range opts {
		o(&cfg)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reg := &Registry{
				drain:   cfg.drain,
				onPanic: cfg.onPanic,
				done:    make(chan struct{}),
			}
			ctx := WithRegistry(r.Context(), reg)
			next.ServeHTTP(w, r.WithContext(ctx))
			reg.Wait()
		})
	}
}
