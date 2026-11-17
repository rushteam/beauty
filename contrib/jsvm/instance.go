package jsvm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"

	"github.com/dop251/goja"
)

type regKind int

const (
	regRoute regKind = iota
	regUse
	regCron
	regCronRemove
	regHook
	regSubscribe
)

// registration 是加载期登记的一次注册,New 完成加载后由首个实例的登记统一挂到 Go 侧。
type registration struct {
	kind   regKind
	method string
	path   string
	name   string // cron 名 / 钩子名 / 事件 topic
	spec   string
	idx    int
	mws    []int
}

// instance 是池中的一个 goja 运行时。同一时刻只被一个 goroutine 使用。
type instance struct {
	vm      *VM
	state   *runtimeState
	id      int
	rt      *goja.Runtime
	fns     []goja.Callable // 注册的处理函数,序号在所有实例间一致
	sigs    []string        // 每个序号的注册描述,用于校验各实例一致
	regs    []registration  // 仅加载期有值
	loading bool
	broken  bool // 执行中发生过 Go panic,归还时重建
	depth   int
	ctx     context.Context // 当前调用的 ctx,供 $http / sleep 等 Go 绑定使用

	modules map[string]*loadedModule // require 缓存,键为 "根id\x00路径"
	frames  []fileOrigin             // 正在执行的入口/模块,决定相对 require 的基准

	stringify goja.Callable
	parse     goja.Callable
}

// prelude 用 var 声明,类才会成为全局对象的属性(Go 侧 rt.Get 可取到)。
const prelude = `
var ApiError = class ApiError extends Error {
	constructor(status, message, data) {
		super(message || "");
		this.name = "ApiError";
		this.status = status;
		this.data = data;
	}
};
var BadRequestError = class BadRequestError extends ApiError { constructor(message, data) { super(400, message || "Bad Request", data); this.name = "BadRequestError"; } };
var UnauthorizedError = class UnauthorizedError extends ApiError { constructor(message, data) { super(401, message || "Unauthorized", data); this.name = "UnauthorizedError"; } };
var ForbiddenError = class ForbiddenError extends ApiError { constructor(message, data) { super(403, message || "Forbidden", data); this.name = "ForbiddenError"; } };
var NotFoundError = class NotFoundError extends ApiError { constructor(message, data) { super(404, message || "Not Found", data); this.name = "NotFoundError"; } };
`

var preludeProgram = goja.MustCompile("jsvm:prelude", prelude, false)

func (vm *VM) newInstance(id int, st *runtimeState) (inst *instance, err error) {
	rt := goja.New()
	rt.SetFieldNameMapper(goja.UncapFieldNameMapper())
	inst = &instance{
		vm: vm, state: st, id: id, rt: rt, loading: true, ctx: vm.lifeCtx,
		modules: map[string]*loadedModule{},
	}

	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("jsvm: panic while loading scripts: %v", p)
		}
	}()

	jsonObj := rt.Get("JSON").ToObject(rt)
	inst.stringify, _ = goja.AssertFunction(jsonObj.Get("stringify"))
	inst.parse, _ = goja.AssertFunction(jsonObj.Get("parse"))

	if _, err := rt.RunProgram(preludeProgram); err != nil {
		return nil, fmt.Errorf("jsvm: prelude: %w", err)
	}
	if err := inst.bindAll(); err != nil {
		return nil, err
	}
	if err := inst.bindRequire(); err != nil {
		return nil, err
	}
	for _, g := range vm.cfg.globals {
		if err := rt.Set(g.name, g.value); err != nil {
			return nil, fmt.Errorf("jsvm: set global %q: %w", g.name, err)
		}
	}
	for _, setup := range vm.cfg.setups {
		if err := setup(rt); err != nil {
			return nil, fmt.Errorf("jsvm: setup: %w", err)
		}
	}

	ctx := context.Background()
	if t := vm.cfg.timeout; t > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t)
		defer cancel()
	}
	stop := interruptOn(ctx, rt)
	defer stop()
	for _, u := range st.units {
		if err := inst.runEntry(u); err != nil {
			return nil, fmt.Errorf("jsvm: load script: %w", wrapInterrupt(err))
		}
	}
	return inst, nil
}

// runEntry 执行一个入口脚本。入口脚本之间共享全局作用域;执行期间压入文件帧,
// 使顶层的 require("./x") 相对该文件解析。
func (inst *instance) runEntry(u scriptUnit) error {
	inst.frames = append(inst.frames, u.origin)
	defer func() { inst.frames = inst.frames[:len(inst.frames)-1] }()
	dir := path.Dir(u.origin.file)
	if dir == "." {
		dir = ""
	}
	_ = inst.rt.Set("__filename", u.origin.file)
	_ = inst.rt.Set("__dirname", dir)
	_, err := inst.rt.RunProgram(u.prog)
	return err
}

// call 执行 fn。最外层调用负责设置 ctx、超时中断与 panic 兜底;JS 调用链内的嵌套调用直接执行。
func (inst *instance) call(ctx context.Context, fn goja.Callable, args ...goja.Value) (v goja.Value, err error) {
	if inst.depth > 0 {
		return fn(goja.Undefined(), args...)
	}
	if t := inst.vm.cfg.timeout; t > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t)
		defer cancel()
	}
	inst.depth++
	inst.ctx = ctx
	stop := interruptOn(ctx, inst.rt)
	defer func() {
		stop()
		inst.depth--
		inst.ctx = inst.vm.lifeCtx
		if p := recover(); p != nil {
			inst.broken = true
			err = fmt.Errorf("jsvm: panic in Go binding: %v", p)
		}
	}()
	return fn(goja.Undefined(), args...)
}

// toError 把 goja 返回的错误转成 *Error(须在持有实例时调用,会读取异常对象)。
func (inst *instance) toError(err error) error {
	if err == nil {
		return nil
	}
	var ex *goja.Exception
	if !errors.As(err, &ex) {
		return wrapInterrupt(err)
	}
	e := &Error{Message: ex.Value().String(), err: err}
	if obj, ok := ex.Value().(*goja.Object); ok {
		if s := obj.Get("status"); isSet(s) {
			e.Status = int(s.ToInteger())
			e.Message = obj.Get("message").String()
			if d := obj.Get("data"); isSet(d) {
				e.Data = d.Export()
			}
		}
	}
	return e
}

// register 在加载期登记一个处理函数并返回其序号。
func (inst *instance) register(kind, sig string, fn goja.Value) int {
	if !inst.loading {
		panic(inst.rt.NewTypeError(kind + " can only be called at script top level during loading"))
	}
	c, ok := goja.AssertFunction(fn)
	if !ok {
		panic(inst.rt.NewTypeError(kind + ": handler must be a function"))
	}
	inst.fns = append(inst.fns, c)
	inst.sigs = append(inst.sigs, kind+" "+sig)
	return len(inst.fns) - 1
}

// logger 返回本实例应使用的 logger:非首个实例在加载期静默,避免同一行日志打印"池大小"次。
func (inst *instance) logger() *slog.Logger {
	if inst.loading && inst.id != 0 {
		return slog.New(slog.DiscardHandler)
	}
	return inst.vm.cfg.logger
}

// throw 把 Go 错误作为 JS 异常抛出(仅在 Go 绑定函数内使用)。
func (inst *instance) throw(err error) {
	panic(inst.rt.NewGoError(err))
}

// rethrow 把嵌套调用返回的错误原样抛回 JS,保留原异常对象与中断信号。
func rethrow(err error) {
	if err != nil {
		panic(err)
	}
}

// apiError 构造一个 JS 侧的 ApiError 实例,供 Go 绑定以 panic 抛出。
func (inst *instance) apiError(status int, message string) *goja.Object {
	obj, err := inst.rt.New(inst.rt.Get("ApiError"), inst.rt.ToValue(status), inst.rt.ToValue(message))
	rethrow(err)
	return obj
}

func (inst *instance) toJSON(v goja.Value) string {
	if !isSet(v) {
		return "null"
	}
	res, err := inst.stringify(goja.Undefined(), v)
	rethrow(err)
	if !isSet(res) {
		return "null"
	}
	return res.String()
}

// toBytes 接受 string / ArrayBuffer / TypedArray / DataView。
func (inst *instance) toBytes(v goja.Value) ([]byte, bool) {
	if !isSet(v) {
		return nil, true
	}
	switch x := v.Export().(type) {
	case string:
		return []byte(x), true
	case []byte:
		return x, true
	case goja.ArrayBuffer:
		return x.Bytes(), true
	}
	var b []byte
	if obj, ok := v.(*goja.Object); ok && obj.ClassName() == "DataView" {
		if inst.rt.ExportTo(v, &b) == nil {
			return b, true
		}
	}
	return nil, false
}

func isSet(v goja.Value) bool {
	return v != nil && !goja.IsUndefined(v) && !goja.IsNull(v)
}
