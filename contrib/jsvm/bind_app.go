package jsvm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/dop251/goja"
)

func (inst *instance) bindAll() error {
	binders := []func() error{
		inst.bindRegistry,
		inst.bindApp,
		inst.bindConsole,
		inst.bindSecurity,
		inst.bindHTTP,
		inst.bindFilesystem,
	}
	if inst.vm.cfg.enableOS {
		binders = append(binders, inst.bindOS)
	}
	for _, b := range binders {
		if err := b(); err != nil {
			return err
		}
	}
	return nil
}

func setAll(target interface{ Set(string, any) error }, kv map[string]any) error {
	for k, v := range kv {
		if err := target.Set(k, v); err != nil {
			return fmt.Errorf("jsvm: bind %q: %w", k, err)
		}
	}
	return nil
}

// bindRegistry 绑定只能在加载期调用的注册函数。
func (inst *instance) bindRegistry() error {
	rt := inst.rt
	hookAdd := func(name string, fn goja.Value) {
		idx := inst.register("hookAdd", name, fn)
		inst.regs = append(inst.regs, registration{kind: regHook, name: name, idx: idx})
	}
	return setAll(rt, map[string]any{
		"routerAdd": func(call goja.FunctionCall) goja.Value {
			method, path := call.Argument(0).String(), call.Argument(1).String()
			sig := method + " " + path
			r := registration{kind: regRoute, method: method, path: path}
			r.idx = inst.register("routerAdd", sig, call.Argument(2))
			for _, mw := range call.Arguments[min(3, len(call.Arguments)):] {
				r.mws = append(r.mws, inst.register("routerAdd.middleware", sig, mw))
			}
			inst.regs = append(inst.regs, r)
			return goja.Undefined()
		},
		"routerUse": func(fn goja.Value) {
			idx := inst.register("routerUse", "", fn)
			inst.regs = append(inst.regs, registration{kind: regUse, idx: idx})
		},
		"cronAdd": func(name, spec string, fn goja.Value) {
			if name == "" {
				panic(rt.NewTypeError("cronAdd: name is required"))
			}
			idx := inst.register("cronAdd", name+" "+spec, fn)
			inst.regs = append(inst.regs, registration{kind: regCron, name: name, spec: spec, idx: idx})
		},
		"cronRemove": func(name string) bool {
			if inst.loading {
				inst.regs = append(inst.regs, registration{kind: regCronRemove, name: name})
				return true
			}
			if inst.vm.cron == nil {
				return false
			}
			return inst.vm.cron.Remove(name)
		},
		"hookAdd":  hookAdd,
		"onStart":  func(fn goja.Value) { hookAdd(HookStart, fn) },
		"onStop":   func(fn goja.Value) { hookAdd(HookStop, fn) },
		"onReload": func(fn goja.Value) { hookAdd(HookReload, fn) },
		"eventSubscribe": func(topic string, fn goja.Value) {
			if inst.vm.cfg.bus == nil {
				panic(rt.NewTypeError("eventSubscribe: event bus not configured (jsvm.WithEventBus)"))
			}
			idx := inst.register("eventSubscribe", topic, fn)
			inst.regs = append(inst.regs, registration{kind: regSubscribe, name: topic, idx: idx})
		},
	})
}

func (inst *instance) bindApp() error {
	rt := inst.rt
	vm := inst.vm

	loggerObj := rt.NewObject()
	for name, level := range map[string]slog.Level{
		"debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError,
	} {
		if err := loggerObj.Set(name, func(call goja.FunctionCall) goja.Value {
			args := make([]any, 0, len(call.Arguments))
			for _, a := range call.Arguments[min(1, len(call.Arguments)):] {
				args = append(args, a.Export())
			}
			inst.logger().Log(inst.ctx, level, call.Argument(0).String(), args...)
			return goja.Undefined()
		}); err != nil {
			return err
		}
	}

	storeObj := rt.NewObject()
	if err := setAll(storeObj, map[string]any{
		"get": func(key string) goja.Value {
			if v, ok := vm.store.Load(key); ok {
				return rt.ToValue(v)
			}
			return goja.Null()
		},
		"set": func(key string, v goja.Value) { vm.store.Store(key, v.Export()) },
		"has": func(key string) bool {
			_, ok := vm.store.Load(key)
			return ok
		},
		"remove": func(key string) { vm.store.Delete(key) },
	}); err != nil {
		return err
	}

	app := rt.NewObject()
	if err := setAll(app, map[string]any{
		"logger": func() *goja.Object { return loggerObj },
		"store":  func() *goja.Object { return storeObj },
		// trigger 在当前实例上同步执行钩子(不借新实例),返回(可能被修改的)event。
		"trigger": func(name string, event goja.Value) goja.Value {
			if !isSet(event) {
				event = rt.NewObject()
			}
			for _, i := range inst.state.hooks[name] {
				_, err := inst.fns[i](goja.Undefined(), event)
				rethrow(err)
			}
			return event
		},
		"publish": func(topic string, payload goja.Value) int {
			if vm.cfg.bus == nil {
				panic(rt.NewTypeError("$app.publish: event bus not configured (jsvm.WithEventBus)"))
			}
			var p any
			if payload != nil {
				p = payload.Export()
			}
			return vm.cfg.bus.Publish(topic, p)
		},
	}); err != nil {
		return err
	}

	return setAll(rt, map[string]any{
		"$app": app,
		// sleep 阻塞当前调用 ms 毫秒,调用被取消/超时时抛出异常。
		"sleep": func(ms int64) {
			t := time.NewTimer(time.Duration(ms) * time.Millisecond)
			defer t.Stop()
			select {
			case <-t.C:
			case <-inst.ctx.Done():
				inst.throw(inst.ctx.Err())
			}
		},
	})
}

func (inst *instance) bindConsole() error {
	console := inst.rt.NewObject()
	for name, level := range map[string]slog.Level{
		"log": slog.LevelInfo, "info": slog.LevelInfo, "debug": slog.LevelDebug,
		"warn": slog.LevelWarn, "error": slog.LevelError,
	} {
		if err := console.Set(name, func(call goja.FunctionCall) goja.Value {
			parts := make([]string, len(call.Arguments))
			for i, a := range call.Arguments {
				parts[i] = inst.display(a)
			}
			inst.logger().Log(inst.ctx, level, strings.Join(parts, " "), slog.String("source", "jsvm"))
			return goja.Undefined()
		}); err != nil {
			return err
		}
	}
	return inst.rt.Set("console", console)
}

// display 把参数格式化成日志文本:对象走 JSON,Error 与基本类型走 String()。
func (inst *instance) display(v goja.Value) string {
	obj, ok := v.(*goja.Object)
	if !ok {
		return v.String()
	}
	if _, isFn := goja.AssertFunction(v); isFn || obj.ClassName() == "Error" {
		return v.String()
	}
	if s, err := inst.stringify(goja.Undefined(), v); err == nil && isSet(s) {
		return s.String()
	}
	return v.String()
}

// errNotConfigured 用于未通过 Option 授予的能力。
func errNotConfigured(what, option string) error {
	return errors.New("jsvm: " + what + " not configured (" + option + ")")
}

// callCtx 返回当前调用的 ctx,并叠加可选的超时(秒)。
func (inst *instance) callCtx(timeoutSec float64) (context.Context, context.CancelFunc) {
	if timeoutSec > 0 {
		return context.WithTimeout(inst.ctx, time.Duration(timeoutSec*float64(time.Second)))
	}
	return context.WithCancel(inst.ctx)
}
