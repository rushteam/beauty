// Package callbacks 提供非侵入的执行切面：在某个执行单元的开始/结束/出错时机
// 触发回调，用于统一注入可观测性（trace、metrics、日志）而不改动业务代码。
//
// 业务侧只需在入口/出口埋三个调用：
//
//	func (s *svc) Do(ctx context.Context, in Input) (Output, error) {
//	    info := &callbacks.RunInfo{Name: "Do", Component: "svc"}
//	    ctx = callbacks.OnStart(ctx, info, in)
//	    out, err := s.do(ctx, in)
//	    if err != nil {
//	        callbacks.OnError(ctx, info, err)
//	        return out, err
//	    }
//	    callbacks.OnEnd(ctx, info, out)
//	    return out, nil
//	}
//
// 观测侧注册 Handler（全局或随 ctx 局部），无需感知业务：
//
//	h := callbacks.NewHandlerBuilder().
//	    OnStart(func(ctx context.Context, info *callbacks.RunInfo, input any) context.Context {
//	        return context.WithValue(ctx, startKey{}, time.Now())
//	    }).
//	    OnEnd(func(ctx context.Context, info *callbacks.RunInfo, output any) context.Context {
//	        cost := time.Since(ctx.Value(startKey{}).(time.Time))
//	        metrics.Observe(info.Name, cost)
//	        return ctx
//	    }).
//	    Build()
//	callbacks.AppendGlobalHandlers(h)
package callbacks

import (
	"context"
	"sync"

	"github.com/rushteam/beauty/pkg/ctxkey"
)

// Timing 表示回调时机。
type Timing int

const (
	TimingStart Timing = iota
	TimingEnd
	TimingError
)

// RunInfo 描述被观测的执行单元。
type RunInfo struct {
	Name      string // 实例/方法名
	Type      string // 类型名
	Component string // 组件类别（如 "http" / "grpc" / "svc"）
}

// Handler 是切面回调处理器。每个方法返回（可能被修改的）ctx，
// 供同一 handler 在后续时机间传递状态（如开始时间）。
type Handler interface {
	OnStart(ctx context.Context, info *RunInfo, input any) context.Context
	OnEnd(ctx context.Context, info *RunInfo, output any) context.Context
	OnError(ctx context.Context, info *RunInfo, err error) context.Context
}

// TimingChecker 是 Handler 可选实现的接口：声明自己关心哪些时机，
// 框架据此跳过不需要的回调，降低开销。未实现则视为关心所有时机。
type TimingChecker interface {
	Needed(ctx context.Context, info *RunInfo, timing Timing) bool
}

var (
	globalMu       sync.RWMutex
	globalHandlers []Handler
)

// AppendGlobalHandlers 注册全局 handler（观测所有埋点）。通常在程序启动时调用一次。
func AppendGlobalHandlers(hs ...Handler) {
	globalMu.Lock()
	defer globalMu.Unlock()
	globalHandlers = append(globalHandlers, hs...)
}

var handlersKey = ctxkey.New[[]Handler]()

// WithHandlers 把局部 handler 附加到 ctx；它们与全局 handler 合并生效，
// 作用范围为该 ctx 派生的调用链。
func WithHandlers(ctx context.Context, hs ...Handler) context.Context {
	if len(hs) == 0 {
		return ctx
	}
	existing, _ := ctxkey.Get(ctx, handlersKey)
	merged := make([]Handler, 0, len(existing)+len(hs))
	merged = append(merged, existing...)
	merged = append(merged, hs...)
	return ctxkey.With(ctx, handlersKey, merged)
}

// effective 返回当前生效的 handler：全局 + ctx 局部。
func effective(ctx context.Context) []Handler {
	local, _ := ctxkey.Get(ctx, handlersKey)
	globalMu.RLock()
	g := globalHandlers
	globalMu.RUnlock()
	if len(g) == 0 {
		return local
	}
	if len(local) == 0 {
		return g
	}
	out := make([]Handler, 0, len(g)+len(local))
	out = append(out, g...)
	out = append(out, local...)
	return out
}

func needed(ctx context.Context, h Handler, info *RunInfo, t Timing) bool {
	if c, ok := h.(TimingChecker); ok {
		return c.Needed(ctx, info, t)
	}
	return true
}

// OnStart 触发所有生效 handler 的 OnStart，返回串联后的 ctx。
func OnStart(ctx context.Context, info *RunInfo, input any) context.Context {
	for _, h := range effective(ctx) {
		if needed(ctx, h, info, TimingStart) {
			ctx = h.OnStart(ctx, info, input)
		}
	}
	return ctx
}

// OnEnd 触发所有生效 handler 的 OnEnd。
func OnEnd(ctx context.Context, info *RunInfo, output any) context.Context {
	for _, h := range effective(ctx) {
		if needed(ctx, h, info, TimingEnd) {
			ctx = h.OnEnd(ctx, info, output)
		}
	}
	return ctx
}

// OnError 触发所有生效 handler 的 OnError。
func OnError(ctx context.Context, info *RunInfo, err error) context.Context {
	for _, h := range effective(ctx) {
		if needed(ctx, h, info, TimingError) {
			ctx = h.OnError(ctx, info, err)
		}
	}
	return ctx
}
