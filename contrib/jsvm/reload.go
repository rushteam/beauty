package jsvm

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dop251/goja"
	"github.com/fsnotify/fsnotify"

	"github.com/rushteam/beauty/pkg/service/cron"
)

// scriptUnit 是一个按顺序执行的入口脚本。origin.root 为 nil 表示内联脚本。
type scriptUnit struct {
	prog   *goja.Program
	origin fileOrigin
}

// runtimeState 是一代脚本:一套编译结果、一个实例池、一份路由与钩子。
// 热重载生成新的一代并整体替换;旧的一代继续服务已经借出实例的在途调用。
type runtimeState struct {
	pool      chan *instance
	units     []scriptUnit
	sigs      []string
	mux       *http.ServeMux
	hooks     map[string][]int
	crons     []cronSpec
	subs      []subSpec
	cronNames []string
	unsubs    []func()
	retired   chan struct{} // 被替换时关闭,唤醒阻塞在这一代上的等待者
}

type cronSpec struct {
	name   string
	spec   string
	idx    int
	remove bool
}

type subSpec struct {
	topic string
	idx   int
}

// Reload 重新读取并编译全部入口脚本,成功后原子切换到新的一代。
//
// 切换时:在途请求、正在执行的定时任务与事件回调继续用旧一代跑完;之后的请求、
// vm.Trigger 和新建的定时任务用新脚本。失败时返回错误并保持旧一代继续服务。
// $app.store() 的内容跨代保留。切换成功后触发 onReload 钩子(钩子失败只记日志)。
func (vm *VM) Reload(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	st, err := vm.prepare()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := vm.commit(st); err != nil {
		return err
	}
	if err := vm.Trigger(ctx, HookReload, nil); err != nil {
		vm.cfg.logger.Error("jsvm: onReload failed", slog.Any("err", err))
	}
	return nil
}

// prepare 编译并执行脚本、建好实例池与路由表,但不触碰正在服务的 cron 与事件订阅。
func (vm *VM) prepare() (_ *runtimeState, err error) {
	units, err := vm.compileSources()
	if err != nil {
		return nil, err
	}
	st := &runtimeState{
		pool:    make(chan *instance, vm.cfg.poolSize),
		units:   units,
		mux:     http.NewServeMux(),
		hooks:   map[string][]int{},
		retired: make(chan struct{}),
	}
	insts := make([]*instance, vm.cfg.poolSize)
	for i := range insts {
		inst, err := vm.newInstance(i, st)
		if err != nil {
			return nil, err
		}
		if i > 0 {
			if err := diffSigs(insts[0].sigs, inst.sigs); err != nil {
				return nil, fmt.Errorf("jsvm: instance %d: %w; registration must not depend on runtime conditions", i, err)
			}
		}
		insts[i] = inst
	}
	st.sigs = insts[0].sigs
	if err := vm.collect(st, insts[0].regs); err != nil {
		return nil, err
	}
	if err := preflightCrons(st.crons); err != nil {
		return nil, err
	}
	for _, inst := range insts {
		inst.loading = false
		inst.regs = nil
		st.pool <- inst
	}
	return st, nil
}

// preflightCrons 在改动正在运行的调度表之前先验证 spec,避免切到一半失败。
func preflightCrons(crons []cronSpec) error {
	if len(crons) == 0 {
		return nil
	}
	tmp := cron.New()
	for _, c := range crons {
		if c.remove {
			continue
		}
		if err := tmp.Add(c.spec, func(context.Context) error { return nil }, cron.HandlerName(c.name)); err != nil {
			return fmt.Errorf("jsvm: cronAdd %q: %w", c.name, err)
		}
	}
	return nil
}

// commit 把一代脚本切换为当前代:替换路由(随 state 一起发布)、调整 cron、重挂事件订阅。
func (vm *VM) commit(st *runtimeState) error {
	vm.reloadMu.Lock()
	defer vm.reloadMu.Unlock()
	vm.mu.Lock()
	closed := vm.closed
	vm.mu.Unlock()
	if closed {
		return errors.New("jsvm: closed")
	}

	names := make([]string, 0, len(st.crons))
	for _, c := range st.crons {
		if c.remove {
			if vm.cron != nil {
				vm.cron.Remove(c.name)
			}
			names = slices.DeleteFunc(names, func(n string) bool { return n == c.name })
			continue
		}
		idx := c.idx
		if err := vm.cronInstance().Add(c.spec, func(ctx context.Context) error {
			return vm.invokeState(ctx, st, idx)
		}, cron.HandlerName(c.name)); err != nil {
			return fmt.Errorf("jsvm: cronAdd %q: %w", c.name, err)
		}
		if !slices.Contains(names, c.name) {
			names = append(names, c.name)
		}
	}
	st.cronNames = names

	vm.mu.Lock()
	if vm.closed {
		vm.mu.Unlock()
		return errors.New("jsvm: closed")
	}
	old := vm.state.Load()
	var oldUnsubs []func()
	var oldNames []string
	if old != nil {
		oldUnsubs = old.unsubs
		old.unsubs = nil
		oldNames = old.cronNames
	}
	vm.mu.Unlock()

	for _, name := range oldNames {
		if !slices.Contains(names, name) && vm.cron != nil {
			vm.cron.Remove(name)
		}
	}
	for _, u := range oldUnsubs {
		u()
	}
	newUnsubs := make([]func(), 0, len(st.subs))
	for _, s := range st.subs {
		newUnsubs = append(newUnsubs, vm.subscribe(st, s.topic, s.idx))
	}

	vm.mu.Lock()
	if vm.closed {
		vm.mu.Unlock()
		for _, u := range newUnsubs {
			u()
		}
		return errors.New("jsvm: closed")
	}
	st.unsubs = newUnsubs
	vm.state.Store(st)
	vm.mu.Unlock()

	if old != nil {
		close(old.retired)
	}
	return nil
}

// collect 把入口脚本登记的路由与钩子挂到这一代自己的 ServeMux 上。
// 全局中间件先于路由收集,与 routerUse 出现的位置无关。
func (vm *VM) collect(st *runtimeState, regs []registration) error {
	var globalMws []int
	for _, r := range regs {
		if r.kind == regUse {
			globalMws = append(globalMws, r.idx)
		}
	}
	for _, r := range regs {
		switch r.kind {
		case regRoute:
			if err := vm.addRoute(st, globalMws, r); err != nil {
				return err
			}
		case regCron:
			st.crons = append(st.crons, cronSpec{name: r.name, spec: r.spec, idx: r.idx})
		case regCronRemove:
			st.crons = append(st.crons, cronSpec{name: r.name, remove: true})
		case regHook:
			st.hooks[r.name] = append(st.hooks[r.name], r.idx)
		case regSubscribe:
			st.subs = append(st.subs, subSpec{topic: r.name, idx: r.idx})
		}
	}
	return nil
}

func (vm *VM) addRoute(st *runtimeState, globalMws []int, r registration) (err error) {
	pattern := r.path
	if m := strings.ToUpper(strings.TrimSpace(r.method)); m != "" && m != "*" {
		pattern = m + " " + r.path
	}
	chain := slices.Concat(globalMws, r.mws, []int{r.idx})
	defer func() {
		// ServeMux 对非法/冲突模式直接 panic。
		if p := recover(); p != nil {
			err = fmt.Errorf("jsvm: routerAdd %q: %v", pattern, p)
		}
	}()
	st.mux.Handle(pattern, vm.routeHandler(st, chain))
	return nil
}

func (vm *VM) compileSources() ([]scriptUnit, error) {
	var units []scriptUnit
	compile := func(origin fileOrigin, code string) error {
		name := vm.scriptName(origin)
		p, err := goja.Compile(name, code, false)
		if err != nil {
			return fmt.Errorf("jsvm: compile %s: %w", origin.file, err)
		}
		units = append(units, scriptUnit{prog: p, origin: origin})
		return nil
	}
	for _, s := range vm.cfg.sources {
		if s.fsys == nil {
			if err := compile(fileOrigin{file: s.name}, s.code); err != nil {
				return nil, err
			}
			continue
		}
		matches, err := fs.Glob(s.fsys, s.pattern)
		if err != nil {
			return nil, fmt.Errorf("jsvm: glob %q: %w", s.pattern, err)
		}
		slices.Sort(matches)
		root := vm.rootByID(s.rootID)
		for _, m := range matches {
			code, err := fs.ReadFile(s.fsys, m)
			if err != nil {
				return nil, fmt.Errorf("jsvm: read %s: %w", m, err)
			}
			if err := compile(fileOrigin{root: root, file: m}, string(code)); err != nil {
				return nil, err
			}
		}
	}
	return units, nil
}

// watch 监听 WithDir 目录,变更经去抖后 Reload。ctx 取消时返回。
func (vm *VM) watch(ctx context.Context) {
	dirs := vm.watchDirs()
	if len(dirs) == 0 {
		vm.cfg.logger.Warn("jsvm: WithWatch has no WithDir to watch")
		<-ctx.Done()
		return
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		vm.cfg.logger.Error("jsvm: watch disabled", slog.Any("err", err))
		return
	}
	defer w.Close()
	if err := addWatches(w, dirs); err != nil {
		vm.cfg.logger.Error("jsvm: watch", slog.Any("err", err))
	}

	var timer *time.Timer
	var timerC <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-timerC:
			timer, timerC = nil, nil
			if err := vm.Reload(context.WithoutCancel(ctx)); err != nil {
				vm.cfg.logger.Error("jsvm: reload failed", slog.Any("err", err))
				continue
			}
			vm.cfg.logger.Info("jsvm: scripts reloaded")
			if err := addWatches(w, dirs); err != nil {
				vm.cfg.logger.Error("jsvm: watch", slog.Any("err", err))
			}
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			if ev.Has(fsnotify.Create) {
				if fi, statErr := os.Stat(ev.Name); statErr == nil && fi.IsDir() {
					_ = addWatches(w, []string{ev.Name})
				}
			}
			if !scriptEvent(ev) {
				continue
			}
			if timer == nil {
				timer = time.NewTimer(vm.cfg.watchDelay)
				timerC = timer.C
				continue
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(vm.cfg.watchDelay)
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			vm.cfg.logger.Error("jsvm: watch", slog.Any("err", err))
		}
	}
}

func (vm *VM) watchDirs() []string {
	var dirs []string
	for _, s := range vm.cfg.sources {
		if s.dir != "" {
			dirs = append(dirs, s.dir)
		}
	}
	return dirs
}

func addWatches(w *fsnotify.Watcher, dirs []string) error {
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return err
			}
			return w.Add(p)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func scriptEvent(ev fsnotify.Event) bool {
	if ev.Op == fsnotify.Chmod {
		return false
	}
	switch strings.ToLower(filepath.Ext(ev.Name)) {
	case ".js", ".json":
		return true
	default:
		return false
	}
}
