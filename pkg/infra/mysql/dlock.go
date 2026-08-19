// Package mysql 基于 MySQL Advisory Lock（GET_LOCK / RELEASE_LOCK）实现 pkg/dlock
// 的 Locker 与 Elector。
//
// MySQL Advisory Lock 是 MySQL 内置的应用级锁：session-level 锁随连接断开自动释放,
// 不需要额外的 TTL/续期机制——进程崩溃 → TCP 连接断 → 锁即释放,failover 语义天然正确。
//
// 依赖：仅 database/sql（标准库），用户自行 import MySQL 驱动
// （如 _ "github.com/go-sql-driver/mysql"）。不引入任何第三方依赖,与 beauty 核心保持一致。
//
// 每次 Lock/TryLock/electOnce 使用一个 dedicated sql.Conn（因为 advisory lock 是
// session-level,绑定到 MySQL 连接,不能跨连接使用），Unlock / 失去 leader 后归还连接。
//
// 注意：MySQL 5.7.5 之前，同一连接同时只能持有一个 advisory lock（GET_LOCK 会隐式
// 释放之前持有的锁）。5.7.5+ 支持同时持有多个锁。本实现要求 MySQL 5.7.5+。
//
// lock name 上限 64 字符（MySQL 限制），超过时自动用 FNV-1a 哈希缩短。
package mysql

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
	defaultPrefix    = "beauty:dlock:"
	defaultRetry     = 2 * time.Second
	defaultHeartbeat = 5 * time.Second
	maxLockNameLen   = 64
)

// DLock 基于 MySQL Advisory Lock 实现 Locker + Elector。零值不可用,用 NewDLock 构造。
type DLock struct {
	db        *sql.DB
	prefix    string
	retry     time.Duration
	heartbeat time.Duration
}

// DLockOption 配置 DLock。
type DLockOption func(*DLock)

// WithPrefix 设置 key 前缀（默认 "beauty:dlock:"），与其他应用/环境隔离锁命名空间。
func WithPrefix(prefix string) DLockOption {
	return func(d *DLock) {
		if prefix != "" {
			d.prefix = prefix
		}
	}
}

// WithRetryInterval 设置阻塞 Lock / 竞选的 GET_LOCK 超时时间（默认 2s）。
// MySQL GET_LOCK 的 timeout 参数为整数秒，不足 1s 的值会被抬到 1s。
func WithRetryInterval(d time.Duration) DLockOption {
	return func(dl *DLock) {
		if d > 0 {
			dl.retry = d
		}
	}
}

// WithHeartbeat 设置 Elector 持有期间的连接健康检测间隔（默认 5s）。
// Ping 失败时 leaderCtx 被 cancel（连接断开 = 锁已释放）。
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

// lockName 将 prefix+key 映射到 MySQL advisory lock 名称（≤64 字符）。
// 短于限制时直接使用原文（方便 SHOW PROCESSLIST 排查），超长时 FNV-1a 哈希。
func (d *DLock) lockName(key string) string {
	name := d.prefix + key
	if len(name) <= maxLockNameLen {
		return name
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))
	return fmt.Sprintf("%016x", h.Sum64())
}

// lockTimeout 将 retry 间隔转为 GET_LOCK 的整数秒 timeout，最小 1s。
func (d *DLock) lockTimeout() int64 {
	t := int64(d.retry.Seconds())
	if t < 1 {
		t = 1
	}
	return t
}

// ---- Locker ----

// Lock 实现 dlock.Locker：在 dedicated 连接上循环调用 GET_LOCK(name, timeout)
// 直到获得锁或 ctx 取消。GET_LOCK 自身是阻塞调用（MySQL 服务端等待），
// 超时返回 0 后继续重试。返回的 Lock 持有该连接——Unlock 释放锁并归还连接。
func (d *DLock) Lock(ctx context.Context, key string) (dlock.Lock, error) {
	name := d.lockName(key)
	conn, err := d.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("mysql dlock: conn: %w", err)
	}
	timeout := d.lockTimeout()

	for {
		if err := ctx.Err(); err != nil {
			conn.Close()
			return nil, err
		}
		var result sql.NullInt64
		if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, ?)", name, timeout).Scan(&result); err != nil {
			conn.Close()
			return nil, fmt.Errorf("mysql dlock: lock %q: %w", key, err)
		}
		if result.Valid && result.Int64 == 1 {
			return &mysqlLock{conn: conn, name: name}, nil
		}
		if !result.Valid {
			// NULL = 内部错误（OOM / 线程被 kill），不应重试
			conn.Close()
			return nil, fmt.Errorf("mysql dlock: lock %q: GET_LOCK returned NULL (internal error)", key)
		}
		// result.Int64 == 0: 超时未获得锁，重试（GET_LOCK 已阻塞 timeout 秒）
	}
}

// TryLock 实现 dlock.Locker：非阻塞 GET_LOCK(name, 0)。
// 已被占用返回 (nil, false, nil)。
func (d *DLock) TryLock(ctx context.Context, key string) (dlock.Lock, bool, error) {
	name := d.lockName(key)
	conn, err := d.db.Conn(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("mysql dlock: conn: %w", err)
	}
	var result sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 0)", name).Scan(&result); err != nil {
		conn.Close()
		return nil, false, fmt.Errorf("mysql dlock: trylock %q: %w", key, err)
	}
	if !result.Valid || result.Int64 != 1 {
		conn.Close()
		return nil, false, nil
	}
	return &mysqlLock{conn: conn, name: name}, true, nil
}

// mysqlLock 是一次成功获取的 advisory lock，Unlock 释放锁并关闭底层连接。
type mysqlLock struct {
	conn *sql.Conn
	name string
	once sync.Once
}

// Unlock 实现 dlock.Lock：RELEASE_LOCK + 关闭连接（幂等）。
// 即使不调用 Unlock，连接关闭（GC / 进程退出）也会自动释放 advisory lock。
func (l *mysqlLock) Unlock(ctx context.Context) error {
	var err error
	l.once.Do(func() {
		_, err = l.conn.ExecContext(ctx, "SELECT RELEASE_LOCK(?)", l.name)
		l.conn.Close()
	})
	return err
}

// ---- Elector ----

// Run 实现 dlock.Elector：循环调用 GET_LOCK 参选，当选后以 dedicated 连接维持
// leader 身份（MySQL advisory lock 随连接存活），定期 Ping 检测连接健康。
// 连接断开或 Ping 失败时 leaderCtx 被 cancel；onElected 返回后若 outer ctx 仍存活
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
	name := d.lockName(key)

	conn, err := d.db.Conn(ctx)
	if err != nil {
		slog.Warn("mysql elector: conn failed, retrying", "key", key, "err", err)
		waitOrCancel(ctx, d.retry)
		return
	}

	timeout := d.lockTimeout()

	for {
		if err := ctx.Err(); err != nil {
			conn.Close()
			return
		}
		var result sql.NullInt64
		if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, ?)", name, timeout).Scan(&result); err != nil {
			conn.Close()
			slog.Warn("mysql elector: GET_LOCK failed, retrying", "key", key, "err", err)
			waitOrCancel(ctx, d.retry)
			return
		}
		if result.Valid && result.Int64 == 1 {
			break
		}
		if !result.Valid {
			conn.Close()
			slog.Warn("mysql elector: GET_LOCK returned NULL, retrying", "key", key)
			waitOrCancel(ctx, d.retry)
			return
		}
		// result.Int64 == 0: 超时，GET_LOCK 已阻塞 timeout 秒，直接重试
	}

	slog.Info("mysql elector: elected", "key", key)

	leaderCtx, cancel := context.WithCancel(ctx)

	// 连接健康检测：MySQL advisory lock 的存活性等于连接存活性。
	go func() {
		ticker := time.NewTicker(d.heartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-leaderCtx.Done():
				return
			case <-ticker.C:
				if err := conn.PingContext(leaderCtx); err != nil {
					slog.Warn("mysql elector: heartbeat failed, lost leadership", "key", key, "err", err)
					cancel()
					return
				}
			}
		}
	}()

	onElected(leaderCtx)
	cancel()

	// Best-effort unlock；连接关闭也会自动释放。
	_, _ = conn.ExecContext(context.Background(), "SELECT RELEASE_LOCK(?)", name)
	conn.Close()
}

// waitOrCancel 等待 d 或 ctx 取消，返回 false 表示 ctx 已取消。
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
	_ dlock.Lock    = (*mysqlLock)(nil)
)
