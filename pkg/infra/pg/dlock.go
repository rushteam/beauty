// Package pg 基于 PostgreSQL Advisory Lock 实现 pkg/dlock 的 Locker 与 Elector。
//
// Advisory Lock 是 PG 内置的轻量级锁:session-level 锁随连接断开自动释放,不需要
// 额外的 TTL/续期机制——进程崩溃 → TCP 连接断 → 锁即释放,failover 语义天然正确。
//
// 依赖:仅 database/sql(标准库),用户自行 import PG 驱动(lib/pq 或 pgx/stdlib)。
// 不引入任何第三方依赖,与 beauty 核心保持一致。
//
// 每次 Lock/TryLock/electOnce 使用一个 dedicated sql.Conn(因为 advisory lock 是
// session-level,绑定到 PG backend process,不能跨连接使用),Unlock / 失去 leader
// 后归还连接。
package pg

import (
	"context"
	"database/sql"
	"fmt"
	"hash/fnv"
	"log/slog"
	"sync"
	"time"

	"github.com/rushteam/beauty/pkg/store/dlock"
)

const (
	defaultPrefix    = "beauty/dlock/"
	defaultRetry     = 2 * time.Second
	defaultHeartbeat = 5 * time.Second
)

// DLock 基于 PG Advisory Lock 实现 Locker + Elector。零值不可用,用 NewDLock 构造。
type DLock struct {
	db        *sql.DB
	prefix    string
	retry     time.Duration
	heartbeat time.Duration
}

// DLockOption 配置 DLock。
type DLockOption func(*DLock)

// WithPrefix 设置 key 前缀(默认 "beauty/dlock/"),与其他应用/环境隔离哈希空间。
func WithPrefix(prefix string) DLockOption {
	return func(d *DLock) {
		if prefix != "" {
			d.prefix = prefix
		}
	}
}

// WithRetryInterval 设置竞选轮询间隔(默认 2s)。
func WithRetryInterval(d time.Duration) DLockOption {
	return func(dl *DLock) {
		if d > 0 {
			dl.retry = d
		}
	}
}

// WithHeartbeat 设置 Elector 持有期间的连接健康检测间隔(默认 5s)。
// Ping 失败时 leaderCtx 被 cancel(连接断开 = 锁已释放)。
func WithHeartbeat(d time.Duration) DLockOption {
	return func(dl *DLock) {
		if d > 0 {
			dl.heartbeat = d
		}
	}
}

// NewDLock 用已有 *sql.DB 创建 DLock。db 由调用方管理生命周期。
func NewDLock(db *sql.DB, opts ...DLockOption) *DLock {
	d := &DLock{
		db:        db,
		prefix:    defaultPrefix,
		retry:     defaultRetry,
		heartbeat: defaultHeartbeat,
	}
	for _, o := range opts {
		o(d)
	}
	return d
}

// hashKey 用 FNV-1a 将 prefix+key 映射到 int64(PG advisory lock 的 bigint key)。
func (d *DLock) hashKey(key string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(d.prefix))
	_, _ = h.Write([]byte(key))
	return int64(h.Sum64())
}

// ---- Locker ----

// Lock 实现 dlock.Locker:在 dedicated 连接上执行 pg_advisory_lock(阻塞直到获得
// 锁或 ctx 取消)。返回的 Lock 持有该连接——Unlock 释放锁并归还连接。
func (d *DLock) Lock(ctx context.Context, key string) (dlock.Lock, error) {
	hash := d.hashKey(key)
	conn, err := d.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("pg dlock: conn: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", hash); err != nil {
		conn.Close()
		return nil, fmt.Errorf("pg dlock: lock %q: %w", key, err)
	}
	return &pgLock{conn: conn, key: hash}, nil
}

// TryLock 实现 dlock.Locker:非阻塞尝试 pg_try_advisory_lock。
// 已被占用返回 (nil, false, nil)。
func (d *DLock) TryLock(ctx context.Context, key string) (dlock.Lock, bool, error) {
	hash := d.hashKey(key)
	conn, err := d.db.Conn(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("pg dlock: conn: %w", err)
	}
	var acquired bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", hash).Scan(&acquired); err != nil {
		conn.Close()
		return nil, false, fmt.Errorf("pg dlock: trylock %q: %w", key, err)
	}
	if !acquired {
		conn.Close()
		return nil, false, nil
	}
	return &pgLock{conn: conn, key: hash}, true, nil
}

// pgLock 是一次成功获取的 advisory lock,Unlock 释放锁并关闭底层连接。
type pgLock struct {
	conn *sql.Conn
	key  int64
	once sync.Once
}

// Unlock 实现 dlock.Lock:pg_advisory_unlock + 关闭连接(幂等)。
// 即使不调用 Unlock,连接关闭(GC / 进程退出)也会自动释放 advisory lock。
func (l *pgLock) Unlock(ctx context.Context) error {
	var err error
	l.once.Do(func() {
		_, err = l.conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", l.key)
		l.conn.Close()
	})
	return err
}

// ---- Elector ----

// Run 实现 dlock.Elector:轮询 pg_try_advisory_lock 参选,当选后以 dedicated 连接
// 维持 leader 身份(PG advisory lock 随连接存活),定期 Ping 检测连接健康。
// 连接断开或 Ping 失败时 leaderCtx 被 cancel;onElected 返回后若 outer ctx 仍存活
// 则重新参选。
func (d *DLock) Run(ctx context.Context, key string, onElected func(leaderCtx context.Context)) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		d.electOnce(ctx, key, onElected)
	}
}

func (d *DLock) electOnce(ctx context.Context, key string, onElected func(leaderCtx context.Context)) {
	hash := d.hashKey(key)

	conn, err := d.db.Conn(ctx)
	if err != nil {
		slog.Warn("pg elector: conn failed, retrying", "key", key, "err", err)
		waitOrCancel(ctx, d.retry)
		return
	}

	for {
		var acquired bool
		if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", hash).Scan(&acquired); err != nil {
			conn.Close()
			slog.Warn("pg elector: try_lock failed, retrying", "key", key, "err", err)
			waitOrCancel(ctx, d.retry)
			return
		}
		if acquired {
			break
		}
		if !waitOrCancel(ctx, d.retry) {
			conn.Close()
			return
		}
	}

	slog.Info("pg elector: elected", "key", key)

	leaderCtx, cancel := context.WithCancel(ctx)

	// 连接健康检测:PG advisory lock 的存活性等于连接存活性。
	go func() {
		ticker := time.NewTicker(d.heartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-leaderCtx.Done():
				return
			case <-ticker.C:
				if err := conn.PingContext(leaderCtx); err != nil {
					slog.Warn("pg elector: heartbeat failed, lost leadership", "key", key, "err", err)
					cancel()
					return
				}
			}
		}
	}()

	onElected(leaderCtx)
	cancel()

	// Best-effort unlock; 连接关闭也会自动释放。
	_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", hash)
	conn.Close()
}

// waitOrCancel 等待 d 或 ctx 取消,返回 false 表示 ctx 已取消。
func waitOrCancel(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

var (
	_ dlock.Locker  = (*DLock)(nil)
	_ dlock.Elector = (*DLock)(nil)
	_ dlock.Lock    = (*pgLock)(nil)
)
