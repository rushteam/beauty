// Package bun 是 beauty 的 Bun ORM 集成,作为**独立 Go 模块**发布
// (github.com/rushteam/beauty/contrib/bun),不进 beauty 核心依赖图。
//
// 提供:
//   - Open / OpenSQLite:按 DSN 建连接,可选只读副本;
//   - 连接池设置、ResilientWrite / ResilientRead(DB 弹性,复用 contrib/sqldb.Guard);
//   - Ping / Close 生命周期。
//
// 边界(机制而非策略):建模、迁移、查询都在使用方——本包只负责把 Bun 按 beauty 约定接好。
package bun

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/mysqldialect"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/schema"
)

// Config 是数据库连接配置。
type Config struct {
	Driver          string        // database/sql 驱动名,如 "sqlite" / "mysql" / "pgx"
	DSN             string        // 主库 DSN
	Replicas        []string      // 只读副本(空则读回退主库)
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration // 默认 1h
	ConnMaxIdleTime time.Duration
}

// DB 持有写/读 Bun 句柄与底层连接池。
type DB struct {
	write   *bun.DB
	read    *bun.DB
	primary *sql.DB
	replica []*sql.DB
}

// Open 打开主库与可选副本。Driver 为空时默认 "sqlite"(配合 file::memory: 或 :memory: 测试)。
func Open(cfg Config) (*DB, error) {
	if cfg.DSN == "" {
		return nil, errors.New("bun: empty DSN")
	}
	driver := cfg.Driver
	if driver == "" {
		driver = "sqlite"
	}
	primary, err := openOne(driver, cfg.DSN, cfg)
	if err != nil {
		return nil, fmt.Errorf("bun: open primary: %w", err)
	}
	db := &DB{
		write:   bun.NewDB(primary, dialectFor(driver)),
		read:    nil,
		primary: primary,
	}
	db.read = db.write

	for i, dsn := range cfg.Replicas {
		r, err := openOne(driver, dsn, cfg)
		if err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("bun: open replica %d: %w", i, err)
		}
		db.replica = append(db.replica, r)
		if i == 0 {
			db.read = bun.NewDB(r, dialectFor(driver))
		}
	}
	return db, nil
}

// OpenSQLite 用纯 Go sqlite 打开(测试/本地)。
func OpenSQLite(dsn string, cfg Config) (*DB, error) {
	cfg.Driver = "sqlite"
	cfg.DSN = dsn
	return Open(cfg)
}

func openOne(driver, dsn string, cfg Config) (*sql.DB, error) {
	sqldb, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}
	if cfg.MaxOpenConns > 0 {
		sqldb.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		sqldb.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	life := cfg.ConnMaxLifetime
	if life == 0 {
		life = time.Hour
	}
	sqldb.SetConnMaxLifetime(life)
	if cfg.ConnMaxIdleTime > 0 {
		sqldb.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
	}
	return sqldb, nil
}

func dialectFor(driver string) schema.Dialect {
	switch driver {
	case "mysql":
		return mysqldialect.New()
	case "pgx", "postgres":
		return pgdialect.New()
	default:
		return sqlitedialect.New()
	}
}

// Write 写句柄(主库)。
func (d *DB) Write() *bun.DB { return d.write }

// Read 读句柄(副本或主库)。
func (d *DB) Read() *bun.DB { return d.read }

// Primary 底层主库 *sql.DB(配置弹性 / 开事务)。
func (d *DB) Primary() *sql.DB { return d.primary }

// Ping 探测主库与副本。
func (d *DB) Ping(ctx context.Context) error {
	if err := d.primary.PingContext(ctx); err != nil {
		return fmt.Errorf("bun: ping primary: %w", err)
	}
	for i, r := range d.replica {
		if err := r.PingContext(ctx); err != nil {
			return fmt.Errorf("bun: ping replica %d: %w", i, err)
		}
	}
	return nil
}

// Close 关闭所有连接。
func (d *DB) Close() error {
	errs := []error{d.primary.Close()}
	for _, r := range d.replica {
		errs = append(errs, r.Close())
	}
	return errors.Join(errs...)
}
