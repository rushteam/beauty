package agent

import (
	"context"
	"fmt"
	"sync"
	"time"
)

const defaultRunLeaseTTL = 30 * time.Second

// claimRun 返回一个会自动续约的 context。未实现 RunCoordinator 的 Store 保持旧行为。
func (r *Runner) claimRun(ctx context.Context, runID string) (context.Context, func(), error) {
	coordinator, ok := r.Store.(RunCoordinator)
	if !ok {
		return ctx, func() {}, nil
	}
	ttl := r.RunLeaseTTL
	if ttl <= 0 {
		ttl = defaultRunLeaseTTL
	}
	owner := r.Name
	if owner == "" {
		owner = "runner"
	}
	owner += ":" + newRunID()
	lease, err := coordinator.Claim(ctx, runID, owner, ttl)
	if err != nil {
		return nil, nil, err
	}

	leaseCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	var leaseMu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		interval := ttl / 2
		if interval <= 0 {
			interval = time.Millisecond
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-leaseCtx.Done():
				return
			case <-ticker.C:
				leaseMu.Lock()
				renewed, err := coordinator.Renew(leaseCtx, runID, lease, ttl)
				if err != nil {
					leaseMu.Unlock()
					cancel()
					return
				}
				lease = renewed
				leaseMu.Unlock()
			}
		}
	}()
	release := func() {
		close(done)
		cancel()
		wg.Wait()
		leaseMu.Lock()
		current := lease
		leaseMu.Unlock()
		if err := coordinator.Release(context.WithoutCancel(ctx), runID, current); err != nil &&
			err != ErrLeaseLost {
			// Release 的失败不能覆盖本轮结果；调用方可从后端日志发现基础设施故障。
			_ = err
		}
	}
	return leaseCtx, release, nil
}

func leaseError(runID string, err error) RunOutcome {
	return outcomeError(runID, nil, nil, fmt.Errorf("agent: claim run %q: %w", runID, err))
}
