// Package jsvm 用 goja(纯 Go 的 ES5.1+/部分 ES6 引擎)以 PocketBase JSVM 的风格把 beauty 的
// 核心能力暴露给 JavaScript,让路由、定时任务、钩子等扩展逻辑可以用脚本编写。
//
// 脚本可用的全局对象:
//
//	routerAdd(method, path, handler, ...middlewares)  注册路由(Go 1.22 ServeMux 模式,method 为 "*" 表示任意)
//	routerUse(middleware)                             对脚本里的所有路由生效的中间件,调用 e.next() 继续
//	cronAdd(name, spec, fn) / cronRemove(name)        定时任务(秒级 spec,同 pkg/service/cron)
//	hookAdd(name, fn) / onStart(fn) / onStop(fn) / onReload(fn)  钩子:Go 侧 VM.Trigger 触发,throw 即中止
//	eventSubscribe(topic, fn)                         订阅 pkg/messaging/eventbus(需 WithEventBus)
//	require(spec)                                    加载模块(相对路径相对当前文件;裸路径按模块根查找)
//	$app      logger() / store() / trigger(name, event) / publish(topic, payload)
//	$http     send({url, method, headers, body, timeout})
//	$security 随机串、摘要/HMAC、AES-GCM、bcrypt、JWT(pkg/security)
//	$filesystem 对象存储读写(pkg/store/blob,需 WithBucket)
//	$os       本地文件 / 环境变量 / 执行命令(需 WithOS(true))
//	console、sleep(ms)、ApiError 及 BadRequestError / UnauthorizedError / ForbiddenError / NotFoundError
//
// 并发模型:goja 实例不可并发,VM 维护一个实例池(WithPoolSize)。每个实例都完整执行一遍脚本,
// 注册函数在每个实例里按顺序登记处理函数,但只有首个实例的登记会真正挂到路由 / cron / 钩子上;
// 调用时借出任意空闲实例,执行它自己那份同序号的函数。因此:
//   - 处理函数可以正常使用闭包和顶层变量,但顶层变量是**每个实例一份**,不共享,跨请求共享状态用 $app.store();
//   - 注册函数只能在加载阶段(脚本顶层)调用,各实例的注册序列必须一致(不要按随机数/时间条件注册);
//   - 顶层代码会执行"池大小"次,应只做注册,避免副作用;
//   - 入口脚本共享一个全局作用域;require 进来的模块有独立作用域,用 module.exports 导出。
//
// 热重载:VM.Reload 重新编译并原子切换到新的一代实例。在途请求继续用旧脚本跑完,
// 之后的请求用新脚本;$app.store() 跨代保留。WithWatch 会在 VM.Start 期间监听 WithDir 并自动 Reload。
//
// 与 contrib/wasm 的关系:两者都是脚本扩展层。wasm 面向多语言、强沙箱、纯计算;jsvm 面向
// "用 JS 快速写业务扩展",默认就能访问网络与存储(由你通过 Option 显式授予)。
package jsvm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/dop251/goja"

	resty "github.com/rushteam/beauty/pkg/client/http"
	"github.com/rushteam/beauty/pkg/service/cron"
)

// 内置钩子名。onStart / onStop / onReload 分别在 VM.Start 开始、停机、Reload 成功切换后触发。
const (
	HookStart  = "beauty.start"
	HookStop   = "beauty.stop"
	HookReload = "beauty.reload"
)

// Error 是脚本执行失败的错误。脚本 throw new ApiError(status, message, data) 时 Status > 0,
// HTTP 路由会据此回写 {code, message, data};其他异常 Status 为 0。
type Error struct {
	Status  int
	Message string
	Data    any
	err     error
}

func (e *Error) Error() string { return "jsvm: " + e.err.Error() }
func (e *Error) Unwrap() error { return e.err }

// VM 是脚本运行时。实现 http.Handler(脚本注册的路由)与 beauty.Service(运行 onStart/onStop
// 与自建 Cron)。用 New 构造,并发安全。
type VM struct {
	cfg     config
	roots   []*moduleRoot
	origins sync.Map // 程序名 → fileOrigin,供 require 从调用栈定位当前文件
	state   atomic.Pointer[runtimeState]

	cron        *cron.Cron
	ownCron     bool
	cronRunning bool
	cronDone    <-chan struct{}
	runCtx      context.Context
	running     bool
	store       sync.Map

	lifeCtx    context.Context
	lifeCancel context.CancelFunc
	mu         sync.Mutex
	reloadMu   sync.Mutex
	closed     bool
	wg         sync.WaitGroup
}

// New 编译并加载脚本,创建实例池,把首个实例的注册挂到路由 / cron / 钩子 / 事件总线上。
// 任一脚本执行失败、注册非法(如路由冲突、spec 非法)或各实例注册序列不一致都返回错误。
func New(opts ...Option) (*VM, error) {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.logger == nil {
		cfg.logger = slog.Default()
	}
	if cfg.httpClient == nil {
		cfg.httpClient = resty.NewHTTPClient()
	}

	vm := &VM{cfg: cfg, cron: cfg.cron}
	vm.lifeCtx, vm.lifeCancel = context.WithCancel(context.Background())
	vm.indexRoots()

	st, err := vm.prepare()
	if err != nil {
		vm.lifeCancel()
		return nil, err
	}
	if err := vm.commit(st); err != nil {
		vm.Close()
		return nil, err
	}
	return vm, nil
}

// diffSigs 比较两个实例的注册序列,返回第一处差异。
func diffSigs(want, got []string) error {
	for i := range min(len(want), len(got)) {
		if want[i] != got[i] {
			return fmt.Errorf("registration #%d is %q, instance 0 has %q", i, got[i], want[i])
		}
	}
	if len(want) != len(got) {
		return fmt.Errorf("registered %d handlers, instance 0 registered %d", len(got), len(want))
	}
	return nil
}

// cronInstance 返回脚本定时任务使用的 Cron,必要时创建。
// VM 已 Start 时,新建的 Cron 会立刻开始运行(Reload 才首次出现定时任务的情况)。
func (vm *VM) cronInstance() *cron.Cron {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	if vm.cron == nil {
		vm.cron = cron.New()
		vm.ownCron = true
		if vm.running {
			vm.startCronLocked()
		}
	}
	return vm.cron
}

// startCronLocked 在持有 vm.mu 时启动自建 Cron,重复调用无效果。
func (vm *VM) startCronLocked() <-chan struct{} {
	if vm.cron == nil || vm.cronRunning || vm.runCtx == nil {
		return vm.cronDone
	}
	vm.cronRunning = true
	done := make(chan struct{})
	vm.cronDone = done
	ctx := vm.runCtx
	go func() {
		defer close(done)
		_ = vm.cron.Start(ctx)
	}()
	return done
}

// subscribe 把 JS 订阅者挂到事件总线,回调固定使用登记它的那一代实例。
// JS 订阅者总是在新 goroutine 里执行:同步总线上若直接借实例,发布方本身正占着实例时会互相等待。
func (vm *VM) subscribe(st *runtimeState, topic string, idx int) func() {
	return vm.cfg.bus.Subscribe(topic, func(topic string, payload any) {
		vm.mu.Lock()
		if vm.closed {
			vm.mu.Unlock()
			return
		}
		vm.wg.Add(1)
		vm.mu.Unlock()
		go func() {
			defer vm.wg.Done()
			if err := vm.invokeState(vm.lifeCtx, st, idx, topic, payload); err != nil {
				vm.cfg.logger.Error("jsvm: event subscriber failed", slog.String("topic", topic), slog.Any("err", err))
			}
		}()
	})
}

// ServeHTTP 分发到脚本注册的路由。可直接作为 webserver.New 的 handler,或挂到已有 mux 下。
func (vm *VM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	vm.state.Load().mux.ServeHTTP(w, r)
}

// Trigger 依次执行 hookAdd(name, ...) 注册的函数,event 以引用方式传给脚本,脚本对它的修改
// 在返回后对 Go 可见。任一函数 throw 即停止并返回 *Error;没有注册时直接返回 nil。
// 使用调用发生时的当前一代脚本。
//
// 不要在 JS 调用链内(例如 WithGlobal 暴露的 Go 函数里)调用 Trigger:实例池耗尽时会等待到 ctx 结束。
// 脚本内触发钩子请用 $app.trigger。
func (vm *VM) Trigger(ctx context.Context, name string, event map[string]any) error {
	if event == nil {
		event = map[string]any{}
	}
	return vm.withInstance(ctx, func(inst *instance) error {
		ev := inst.rt.ToValue(event)
		for _, i := range inst.state.hooks[name] {
			if _, err := inst.call(ctx, inst.fns[i], ev); err != nil {
				return inst.toError(err)
			}
		}
		return nil
	})
}

// HasHook 报告当前一代脚本是否注册了 name 钩子,便于 Go 侧在热路径上跳过构造事件。
func (vm *VM) HasHook(name string) bool {
	return len(vm.state.Load().hooks[name]) > 0
}

// invokeState 从指定一代借一个实例执行序号为 idx 的处理函数。
func (vm *VM) invokeState(ctx context.Context, st *runtimeState, idx int, args ...any) error {
	return vm.withInstanceState(ctx, st, func(inst *instance) error {
		vals := make([]goja.Value, len(args))
		for i, a := range args {
			vals[i] = inst.rt.ToValue(a)
		}
		_, err := inst.call(ctx, inst.fns[idx], vals...)
		return inst.toError(err)
	})
}

// withInstance 借出当前一代的实例执行 f。一代在等待期间被替换时,改借新一代。
func (vm *VM) withInstance(ctx context.Context, f func(*instance) error) error {
	for {
		st := vm.state.Load()
		inst, retry, err := vm.borrow(ctx, st, true)
		if err != nil {
			return err
		}
		if retry {
			continue
		}
		return vm.finish(st, inst, f)
	}
}

// withInstanceState 从指定一代借实例。在途请求与旧的定时任务用它,重载后仍能跑完。
func (vm *VM) withInstanceState(ctx context.Context, st *runtimeState, f func(*instance) error) error {
	inst, _, err := vm.borrow(ctx, st, false)
	if err != nil {
		return err
	}
	return vm.finish(st, inst, f)
}

func (vm *VM) borrow(ctx context.Context, st *runtimeState, follow bool) (*instance, bool, error) {
	select {
	case inst := <-st.pool:
		if follow && vm.state.Load() != st {
			vm.releaseTo(st, inst)
			return nil, true, nil
		}
		return inst, false, nil
	case <-st.retired:
		if follow {
			return nil, true, nil
		}
		// 钉住旧一代的调用方不该走这里:retired 与 pool 同时就绪时 select 可能选中它。
		// 再试一次池子,池子空了就等 ctx。
		select {
		case inst := <-st.pool:
			return inst, false, nil
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	case <-ctx.Done():
		return nil, false, ctx.Err()
	}
}

func (vm *VM) finish(st *runtimeState, inst *instance, f func(*instance) error) error {
	defer vm.releaseTo(st, inst)
	return f(inst)
}

// releaseTo 把实例归还它所属的一代。执行中发生过 Go panic 的实例内部状态不可信:
// 当前代会按脚本重建,已被替换的旧代直接丢弃。
func (vm *VM) releaseTo(st *runtimeState, inst *instance) {
	if inst.broken {
		if vm.state.Load() != st {
			return
		}
		fresh, err := vm.newInstance(inst.id, st)
		if err == nil {
			err = diffSigs(st.sigs, fresh.sigs)
		}
		switch {
		case err != nil:
			vm.cfg.logger.Error("jsvm: rebuild instance failed, keep using the old one", slog.Int("id", inst.id), slog.Any("err", err))
			inst.broken = false
		default:
			fresh.loading, fresh.regs = false, nil
			inst = fresh
		}
	}
	st.pool <- inst
}

// Start 触发 onStart 钩子(失败则返回错误、让应用整体退出),运行自建的 Cron 与文件监听,
// ctx 结束后触发 onStop 钩子并 Close。
func (vm *VM) Start(ctx context.Context) error {
	vm.mu.Lock()
	vm.runCtx = ctx
	vm.running = true
	cronDone := vm.startCronLocked()
	vm.mu.Unlock()

	if err := vm.Trigger(ctx, HookStart, nil); err != nil {
		vm.Close()
		return err
	}
	var watchDone <-chan struct{}
	if vm.cfg.watch {
		ch := make(chan struct{})
		watchDone = ch
		go func() {
			defer close(ch)
			vm.watch(ctx)
		}()
	}
	<-ctx.Done()
	if watchDone != nil {
		<-watchDone
	}

	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), vm.cfg.stopTimeout)
	defer cancel()
	if err := vm.Trigger(stopCtx, HookStop, nil); err != nil {
		vm.cfg.logger.Error("jsvm: onStop failed", slog.Any("err", err))
	}
	vm.mu.Lock()
	cronDone = vm.cronDone
	vm.mu.Unlock()
	if cronDone != nil {
		<-cronDone
	}
	vm.Close()
	return nil
}

func (vm *VM) String() string { return "jsvm" }

// Close 退订事件总线、取消在途的异步订阅者调用并等待其结束。幂等。
// Close 后路由与 Trigger 仍可用(实例不销毁),适合与 webserver 的停机顺序解耦。
func (vm *VM) Close() {
	vm.mu.Lock()
	if vm.closed {
		vm.mu.Unlock()
		return
	}
	vm.closed = true
	var unsubs []func()
	if st := vm.state.Load(); st != nil {
		unsubs = st.unsubs
		st.unsubs = nil
	}
	vm.mu.Unlock()

	for _, u := range unsubs {
		u()
	}
	vm.lifeCancel()
	vm.wg.Wait()
}

// interruptOn 在 ctx 结束时中断 rt。返回的 stop 必须在 JS 执行结束后调用,确保不会把中断标志
// 遗留给下一次调用。
func interruptOn(ctx context.Context, rt *goja.Runtime) (stop func()) {
	done := make(chan struct{})
	cancel := context.AfterFunc(ctx, func() {
		rt.Interrupt(ctx.Err())
		close(done)
	})
	return func() {
		if !cancel() {
			<-done
			rt.ClearInterrupt()
		}
	}
}

func wrapInterrupt(err error) error {
	var ie *goja.InterruptedError
	if errors.As(err, &ie) {
		if cause, ok := ie.Value().(error); ok {
			return fmt.Errorf("jsvm: interrupted: %w", cause)
		}
	}
	return err
}
