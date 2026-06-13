package callbacks

import "context"

// StartFn / EndFn / ErrorFn 是各时机的回调函数签名。
type (
	StartFn func(ctx context.Context, info *RunInfo, input any) context.Context
	EndFn   func(ctx context.Context, info *RunInfo, output any) context.Context
	ErrorFn func(ctx context.Context, info *RunInfo, err error) context.Context
)

// HandlerBuilder 用函数快速构建 Handler。只设置需要的时机即可；
// 构建出的 Handler 会实现 TimingChecker，自动跳过未设置的时机（零开销）。
type HandlerBuilder struct {
	onStart StartFn
	onEnd   EndFn
	onError ErrorFn
}

// NewHandlerBuilder 创建一个 HandlerBuilder。
func NewHandlerBuilder() *HandlerBuilder { return &HandlerBuilder{} }

// OnStart 设置开始时机回调。
func (b *HandlerBuilder) OnStart(fn StartFn) *HandlerBuilder { b.onStart = fn; return b }

// OnEnd 设置结束时机回调。
func (b *HandlerBuilder) OnEnd(fn EndFn) *HandlerBuilder { b.onEnd = fn; return b }

// OnError 设置出错时机回调。
func (b *HandlerBuilder) OnError(fn ErrorFn) *HandlerBuilder { b.onError = fn; return b }

// Build 构建 Handler。
func (b *HandlerBuilder) Build() Handler {
	return &builtHandler{onStart: b.onStart, onEnd: b.onEnd, onError: b.onError}
}

type builtHandler struct {
	onStart StartFn
	onEnd   EndFn
	onError ErrorFn
}

func (h *builtHandler) OnStart(ctx context.Context, info *RunInfo, input any) context.Context {
	if h.onStart != nil {
		return h.onStart(ctx, info, input)
	}
	return ctx
}

func (h *builtHandler) OnEnd(ctx context.Context, info *RunInfo, output any) context.Context {
	if h.onEnd != nil {
		return h.onEnd(ctx, info, output)
	}
	return ctx
}

func (h *builtHandler) OnError(ctx context.Context, info *RunInfo, err error) context.Context {
	if h.onError != nil {
		return h.onError(ctx, info, err)
	}
	return ctx
}

// Needed 实现 TimingChecker：只在对应回调被设置时返回 true。
func (h *builtHandler) Needed(_ context.Context, _ *RunInfo, t Timing) bool {
	switch t {
	case TimingStart:
		return h.onStart != nil
	case TimingEnd:
		return h.onEnd != nil
	case TimingError:
		return h.onError != nil
	default:
		return false
	}
}
