package bun

import (
	"context"

	sqldb "github.com/rushteam/beauty/contrib/sqldb"
	"github.com/uptrace/bun"
)

const resilienceAdmittedKey = "beauty/bun/resilience/admitted"

// ResilienceHook 在 Bun QueryHook 中接入 sqldb.Guard。
type ResilienceHook struct {
	guard *sqldb.Guard
}

// NewResilienceHook 创建 Bun 弹性 Hook。
func NewResilienceHook(cfg sqldb.PathResilience) *ResilienceHook {
	return &ResilienceHook{guard: sqldb.NewGuard(cfg)}
}

func (h *ResilienceHook) BeforeQuery(ctx context.Context, event *bun.QueryEvent) context.Context {
	if err := h.guard.Admit(ctx); err != nil {
		cctx, cancel := context.WithCancelCause(ctx)
		cancel(err)
		return cctx
	}
	if event.Stash == nil {
		event.Stash = make(map[any]any)
	}
	event.Stash[resilienceAdmittedKey] = true
	return ctx
}

func (h *ResilienceHook) AfterQuery(ctx context.Context, event *bun.QueryEvent) {
	if event.Stash == nil {
		return
	}
	admitted, _ := event.Stash[resilienceAdmittedKey].(bool)
	if !admitted {
		return
	}
	h.guard.Finish(event.Err)
}

// ResilientWrite 返回带写路径弹性保护的 Bun 句柄。
func (d *DB) ResilientWrite(cfg sqldb.PathResilience) *bun.DB {
	if cfg.Pool == nil {
		cfg.Pool = d.primary
	}
	return d.write.WithQueryHook(NewResilienceHook(cfg))
}

// ResilientRead 返回带读路径弹性保护的 Bun 句柄。
func (d *DB) ResilientRead(cfg sqldb.PathResilience) *bun.DB {
	if cfg.Pool == nil {
		cfg.Pool = d.primary
	}
	return d.read.WithQueryHook(NewResilienceHook(cfg))
}
