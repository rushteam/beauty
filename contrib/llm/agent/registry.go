package agent

import (
	"context"
	"errors"
	"sync"
	"time"
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

// Registry 是按 requestID 追踪运行中 agent 调用的取消/状态注册表。
//
//	ctx, finish, ok := reg.Start(r.Context(), requestID, 30*time.Second)
//	out := runner.Run(ctx, req)
//	finish(out.Err) // Done 时传 nil;Paused 可 finish(nil) 并自行记 StatusPaused
type Registry struct {
	mu sync.Mutex
	m  map[string]*runEntry
}

// NewRegistry 创建一个空注册表。
func NewRegistry() *Registry {
	return &Registry{m: make(map[string]*runEntry)}
}

// Start 为 id 注册一次运行。
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
			case errors.Is(err, ErrPaused):
				e.info = RunInfo{Status: StatusPaused, Err: err}
			default:
				e.info = RunInfo{Status: StatusError, Err: err}
			}
			reg.mu.Unlock()
		})
	}
	return cctx, finish, true
}

// MarkPaused 将运行中记录标为 Paused(不 cancel ctx)。找不到或非 running 返回 false。
func (reg *Registry) MarkPaused(id string) bool {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	e, ok := reg.m[id]
	if !ok || e.info.Status != StatusRunning {
		return false
	}
	e.info = RunInfo{Status: StatusPaused}
	return true
}

// Cancel 取消 id 对应的运行中记录。
func (reg *Registry) Cancel(id string) bool {
	reg.mu.Lock()
	e, ok := reg.m[id]
	if !ok || (e.info.Status != StatusRunning && e.info.Status != StatusPaused) {
		reg.mu.Unlock()
		return false
	}
	reg.mu.Unlock()
	e.cancel()
	return true
}

// Status 返回 id 对应运行的当前快照。
func (reg *Registry) Status(id string) (RunInfo, bool) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	e, ok := reg.m[id]
	if !ok {
		return RunInfo{}, false
	}
	return e.info, true
}

// Forget 移除已结束(非 StatusRunning)的记录。Paused 可被 Forget。
func (reg *Registry) Forget(id string) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if e, ok := reg.m[id]; ok && e.info.Status != StatusRunning {
		delete(reg.m, id)
	}
}
