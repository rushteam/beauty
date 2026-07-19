// Package sqldb 是 beauty 的 database/sql 读写分离 + OTel 集成,作为**独立 Go 模块**发布
// (github.com/rushteam/beauty/contrib/sqldb),不进 beauty 核心依赖图。它与 sqlc 生成的代码
// 天然配合——sqlc 生成的 Queries 接受一个 DBTX 接口,本模块的 Writer()/Reader() 正是 DBTX。
// 也可用于 sqlx / 手写 database/sql。
//
// 读写分离:主库(primary)承接写,只读副本(replicas)轮询承接读。两种用法:
//   - 显式(推荐、正确):Writer() 给写、Reader() 给读,各自 sqlc.New(...):
//     wq := db.Query(sqldb.Writer(d))  // 伪代码;实际 sqlc.New(d.Writer())
//   - 自动路由(方便,有坑):RW() 按方法猜(Exec→主、Query→从)。注意 INSERT...RETURNING
//     (走 QueryRow 却是写)与 SELECT...FOR UPDATE(读却要走主)会被路由错——这类语句用
//     Primary(ctx) 强制走主库。
//
// 边界(机制而非策略):不 import 数据库驱动(使用方自行空导入,如
// _ "github.com/go-sql-driver/mysql");建模、迁移、查询 SQL(交给 sqlc)都在使用方。
// otelsql 提供命令级 trace/metrics(用 beauty telemetry 的全局 Provider,未配则 no-op)。
package sqldb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/XSAM/otelsql"
)

// DBTX 与 sqlc 生成代码期望的接口一致(database/sql)。*sql.DB、*sql.Tx 均满足;
// 本模块的 Writer()/Reader()/RW() 返回值亦满足,可直接传给 sqlc.New(...)。
type DBTX interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	PrepareContext(context.Context, string) (*sql.Stmt, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Config 是连接配置。Driver 是已注册的 database/sql 驱动名(使用方负责空导入驱动)。
type Config struct {
	Driver      string   // 如 "mysql" / "pgx" / "sqlite"
	PrimaryDSN  string   // 主库 DSN(写)
	ReplicaDSNs []string // 只读副本 DSN(读;空则读也走主库)

	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration // 默认 1h
	ConnMaxIdleTime time.Duration

	DisableTracing bool // 关闭 otelsql 链路(默认开)
	DisableMetrics bool // 关闭 DB stats metrics(默认开)
}

// DB 持有主库与副本连接,提供读写分离的 DBTX。
type DB struct {
	primary  *sql.DB
	replicas []*sql.DB
	next     atomic.Uint64
}

// Open 打开主库与副本连接(经 otelsql 埋点),应用连接池设置。
func Open(cfg Config) (*DB, error) {
	if cfg.Driver == "" || cfg.PrimaryDSN == "" {
		return nil, errors.New("sqldb: Driver and PrimaryDSN are required")
	}
	primary, err := openOne(cfg, cfg.PrimaryDSN)
	if err != nil {
		return nil, fmt.Errorf("sqldb: open primary: %w", err)
	}
	db := &DB{primary: primary}
	for i, dsn := range cfg.ReplicaDSNs {
		r, err := openOne(cfg, dsn)
		if err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("sqldb: open replica %d: %w", i, err)
		}
		db.replicas = append(db.replicas, r)
	}
	return db, nil
}

func openOne(cfg Config, dsn string) (*sql.DB, error) {
	var (
		db  *sql.DB
		err error
	)
	if cfg.DisableTracing {
		db, err = sql.Open(cfg.Driver, dsn)
	} else {
		db, err = otelsql.Open(cfg.Driver, dsn)
	}
	if err != nil {
		return nil, err
	}
	if cfg.MaxOpenConns > 0 {
		db.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		db.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	life := cfg.ConnMaxLifetime
	if life == 0 {
		life = time.Hour
	}
	db.SetConnMaxLifetime(life)
	if cfg.ConnMaxIdleTime > 0 {
		db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
	}
	if !cfg.DisableMetrics {
		_, _ = otelsql.RegisterDBStatsMetrics(db) // 全局 MeterProvider,未配则 no-op
	}
	return db, nil
}

// Writer 返回写句柄(主库)。写、以及需要读己之写/强一致的读用它。
func (db *DB) Writer() DBTX { return db.primary }

// Reader 返回读句柄:有副本则在副本间轮询,否则回退主库。
func (db *DB) Reader() DBTX {
	if len(db.replicas) == 0 {
		return db.primary
	}
	i := db.next.Add(1) - 1
	return db.replicas[int(i%uint64(len(db.replicas)))]
}

// Primary 返回底层主库 *sql.DB,用于开启事务(BeginTx)、迁移等。
func (db *DB) Primary() *sql.DB { return db.primary }

// RW 返回一个**自动路由**的 DBTX:Exec→主库,Query/QueryRow→读句柄(除非 Primary(ctx))。
// 便利但有坑:INSERT...RETURNING(QueryRow 却是写)、SELECT...FOR UPDATE(读却要走主)会被
// 路由错——这类调用请用 Primary(ctx) 强制走主库,或直接用 Writer()。
func (db *DB) RW() DBTX { return &router{db: db} }

// Ping 探测主库与所有副本连通性。
func (db *DB) Ping(ctx context.Context) error {
	if err := db.primary.PingContext(ctx); err != nil {
		return fmt.Errorf("sqldb: ping primary: %w", err)
	}
	for i, r := range db.replicas {
		if err := r.PingContext(ctx); err != nil {
			return fmt.Errorf("sqldb: ping replica %d: %w", i, err)
		}
	}
	return nil
}

// Close 关闭主库与所有副本。
func (db *DB) Close() error {
	errs := []error{db.primary.Close()}
	for _, r := range db.replicas {
		errs = append(errs, r.Close())
	}
	return errors.Join(errs...)
}

// ===== 自动路由 DBTX =====

type primaryKey struct{}

// Primary 标记 ctx:让 RW() 的读也走主库(用于 RETURNING / FOR UPDATE / 读己之写)。
func Primary(ctx context.Context) context.Context {
	return context.WithValue(ctx, primaryKey{}, true)
}

// IsPrimary 返回 ctx 是否被标记为强制走主库。
func IsPrimary(ctx context.Context) bool {
	v, _ := ctx.Value(primaryKey{}).(bool)
	return v
}

type router struct{ db *DB }

func (r *router) read(ctx context.Context) DBTX {
	if IsPrimary(ctx) {
		return r.db.primary
	}
	return r.db.Reader()
}

func (r *router) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return r.db.primary.ExecContext(ctx, q, args...) // 写:主库
}

func (r *router) PrepareContext(ctx context.Context, q string) (*sql.Stmt, error) {
	return r.db.primary.PrepareContext(ctx, q) // 预编译语句默认走主库(保守)
}

func (r *router) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return r.read(ctx).QueryContext(ctx, q, args...)
}

func (r *router) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	return r.read(ctx).QueryRowContext(ctx, q, args...)
}
