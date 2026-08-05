package agent

import (
	"context"
	"errors"
	"sync"
	"time"
)

// RunStatus 是 Registry 追踪的一次运行状态。
type RunStatus string

const (
	StatusRunning   RunStatus = "running"
	StatusDone      RunStatus = "done"
	StatusCancelled RunStatus = "cancelled"
	StatusError     RunStatus = "error"
)

// RunInfo 是 Registry.Status 返回的一次运行快照。
type RunInfo struct {
	Status RunStatus
	Err    error
}

type runEntry struct {
	cancel context.CancelFunc
	info   RunInfo
}

// Registry 是按 requestID 追踪运行中 agent 调用的取消/状态注册表:Start 用 ctx 超时/取消
// 包一层调用方 ctx 并记录下来,外部可凭 id 调 Cancel 主动中止、或调 Status 查询进度,
// 无需持有原始 ctx 或所在 goroutine 的引用。取消是协作式的——依赖 Runner.run 每步已有的
// ctx.Err() 检查(以及 Client.Generate/Stream 自身对 ctx 的响应),Registry 本身不强制中断。
// 并发安全,map+mutex 实现。
//
// 典型用法(HTTP 层):
//
//	ctx, finish, ok := reg.Start(r.Context(), requestID, 30*time.Second)
//	if !ok { /* id 冲突:已有同 id 的运行 */ }
//	resp, err := runner.Run(ctx, req)
//	finish(err)
//
// 另一个请求可并发调用 reg.Cancel(requestID) 或 reg.Status(requestID)。
type Registry struct {
	mu sync.Mutex
	m  map[string]*runEntry
}

// NewRegistry 创建一个空注册表。
func NewRegistry() *Registry {
	return &Registry{m: make(map[string]*runEntry)}
}

// Start 为 id 注册一次运行:parent 被 WithTimeout(timeout>0)或 WithCancel(timeout<=0)包一层,
// 返回的 ctx 应替代 parent 传给 Run/RunStream。finish 必须在运行结束后调用且仅调用一次
// (通常 defer),用于落定终态并释放底层 cancel;传入 nil 表示正常完成,否则记录该 error
// (context.Canceled 归为 StatusCancelled,其余归为 StatusError)。
// 若 id 已有一条尚未 finish 的运行记录,ok=false 且不会创建新记录。
func (reg *Registry) Start(parent context.Context, id string, timeout time.Duration) (ctx context.Context, finish func(err error), ok bool) {
	reg.mu.Lock()
	if e, exists := reg.m[id]; exists && e.info.Status == StatusRunning {
		reg.mu.Unlock()
		return nil, nil, false
	}
	var cctx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		cctx, cancel = context.WithTimeout(parent, timeout)
	} else {
		cctx, cancel = context.WithCancel(parent)
	}
	e := &runEntry{cancel: cancel, info: RunInfo{Status: StatusRunning}}
	reg.m[id] = e
	reg.mu.Unlock()

	var once sync.Once
	finish = func(err error) {
		once.Do(func() {
			cancel()
			reg.mu.Lock()
			switch {
			case err == nil:
				e.info = RunInfo{Status: StatusDone}
			case errors.Is(err, context.Canceled):
				e.info = RunInfo{Status: StatusCancelled, Err: err}
			default:
				e.info = RunInfo{Status: StatusError, Err: err}
			}
			reg.mu.Unlock()
		})
	}
	return cctx, finish, true
}

// Cancel 取消 id 对应的运行中记录(实际调用其 ctx 的 cancel;最终状态由该运行的 finish
// 观察到的 err 落定,通常会是 StatusCancelled)。找不到或已结束返回 false。
func (reg *Registry) Cancel(id string) bool {
	reg.mu.Lock()
	e, ok := reg.m[id]
	if !ok || e.info.Status != StatusRunning {
		reg.mu.Unlock()
		return false
	}
	reg.mu.Unlock()
	e.cancel()
	return true
}

// Status 返回 id 对应运行的当前快照;找不到返回 ok=false。
func (reg *Registry) Status(id string) (RunInfo, bool) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	e, ok := reg.m[id]
	if !ok {
		return RunInfo{}, false
	}
	return e.info, true
}

// Forget 移除 id 对应的记录;仅对已结束(非 StatusRunning)的记录生效,运行中的记录会被忽略
// (避免误删仍持有 cancel 的运行)。调用方应在消费完终态(如把状态返回给客户端后)显式调用,
// 否则记录会一直留在内存里。
func (reg *Registry) Forget(id string) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if e, ok := reg.m[id]; ok && e.info.Status != StatusRunning {
		delete(reg.m, id)
	}
}
