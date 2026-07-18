// Package gorm 是 beauty 的 GORM 集成,作为**独立 Go 模块**发布
// (github.com/rushteam/beauty/contrib/gorm),不进 beauty 核心的依赖图——用框架的人
// 可以不引它、自己实现持久层、或钉不同版本。它不 import beauty 核心,只依赖标准库 slog
// (beauty 的 logger 会喂进来)与 OTel 全局 Provider(beauty 的 telemetry 会配好),
// 因此与框架松耦合、也能脱离框架单用。
//
// 提供:
//   - Open:按 DSN 建 MySQL 连接(读写分离用 Replicas),或 OpenWith 传任意 gorm.Dialector
//     (Postgres/SQLite/测试);
//   - 连接池设置、PrepareStmt、TranslateError(把驱动错误翻成 gorm 语义错误);
//   - otelgorm 链路追踪(Tracing 开关)、gorm→slog 日志桥(含慢查询告警);
//   - DB.Write()/Read() 手动切主从、Close() 优雅关闭;IsDuplicatedKey 错误判定。
//
// 边界(机制而非策略):建模、迁移、仓储模式、事务编排都在使用方——本包只负责"把 GORM
// 按 beauty 的可观测/配置约定接好"。
package gorm

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/uptrace/opentelemetry-go-extra/otelgorm"
	driver "gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/plugin/dbresolver"
)

// Config 是数据库连接配置。DSN 必填;Replicas 非空则开启读写分离(主写、从读)。
type Config struct {
	DSN             string        // 主库 DSN
	Replicas        []string      // 只读副本 DSN(空则不分离)
	MaxOpenConns    int           // 最大连接数(默认 不限,建议设)
	MaxIdleConns    int           // 最大空闲连接(默认 2)
	ConnMaxLifetime time.Duration // 连接最大存活(默认 1h)
	ConnMaxIdleTime time.Duration // 连接最大空闲

	// 以下均为「默认开、置 true 关闭」——零值即推荐默认。
	DisablePrepareStmt bool          // 关闭预编译语句缓存(默认开)
	DisableTracing     bool          // 关闭 otelgorm 链路追踪(默认开)
	SkipTranslateError bool          // 关闭错误翻译(默认开,便于拿到 gorm.ErrDuplicatedKey 等)
	SlowThreshold      time.Duration // 慢查询阈值(默认 200ms;0 用默认)
}

// Option 覆盖默认装配(如自定义 gorm.Config 或 slog logger)。
type Option func(*options)

type options struct {
	logger      *slog.Logger
	gormConfig  func(*gorm.Config)
	resolverCfg func(*dbresolver.Config)
}

// WithLogger 指定 slog.Logger(默认 slog.Default())。
func WithLogger(l *slog.Logger) Option {
	return func(o *options) {
		if l != nil {
			o.logger = l
		}
	}
}

// WithGormConfig 在构造前微调 gorm.Config。
func WithGormConfig(fn func(*gorm.Config)) Option {
	return func(o *options) { o.gormConfig = fn }
}

// WithResolverConfig 在注册读写分离前微调 dbresolver.Config(如改选主策略)。
func WithResolverConfig(fn func(*dbresolver.Config)) Option {
	return func(o *options) { o.resolverCfg = fn }
}

// DB 包一层 *gorm.DB,补充主从切换与优雅关闭。嵌入 *gorm.DB,GORM 全部方法直接可用。
type DB struct {
	*gorm.DB
}

// Open 按 Config 用 MySQL 驱动建连接(读写分离用 Config.Replicas)。
func Open(cfg Config, opts ...Option) (*DB, error) {
	if cfg.DSN == "" {
		return nil, errors.New("gorm: empty DSN")
	}
	primary := driver.Open(cfg.DSN)
	replicas := make([]gorm.Dialector, 0, len(cfg.Replicas))
	for _, dsn := range cfg.Replicas {
		replicas = append(replicas, driver.Open(dsn))
	}
	return OpenWith(primary, replicas, cfg, opts...)
}

// OpenWith 用任意 gorm.Dialector 建连接(Postgres/SQLite/测试等)。primary 是主库,
// replicas 为空则不做读写分离。
func OpenWith(primary gorm.Dialector, replicas []gorm.Dialector, cfg Config, opts ...Option) (*DB, error) {
	o := options{logger: slog.Default()}
	for _, opt := range opts {
		opt(&o)
	}

	gcfg := &gorm.Config{
		PrepareStmt:    !cfg.DisablePrepareStmt,
		TranslateError: !cfg.SkipTranslateError,
		Logger:         newSlogLogger(o.logger, cfg.slowThreshold()),
	}
	if o.gormConfig != nil {
		o.gormConfig(gcfg)
	}

	db, err := gorm.Open(primary, gcfg)
	if err != nil {
		return nil, err
	}

	if len(replicas) > 0 {
		rc := dbresolver.Config{
			Replicas:          replicas,
			Policy:            dbresolver.RandomPolicy{},
			TraceResolverMode: true,
		}
		if o.resolverCfg != nil {
			o.resolverCfg(&rc)
		}
		if err := db.Use(dbresolver.Register(rc)); err != nil {
			return nil, err
		}
	}

	if !cfg.DisableTracing {
		if err := db.Use(otelgorm.NewPlugin()); err != nil {
			return nil, err
		}
	}

	if err := applyPool(db, cfg); err != nil {
		return nil, err
	}
	return &DB{DB: db}, nil
}

func applyPool(db *gorm.DB, cfg Config) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	if cfg.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	life := cfg.ConnMaxLifetime
	if life == 0 {
		life = time.Hour
	}
	sqlDB.SetConnMaxLifetime(life)
	if cfg.ConnMaxIdleTime > 0 {
		sqlDB.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
	}
	return nil
}

// Write 显式走主库(读写分离下)。无副本时等价于普通 DB。
func (d *DB) Write() *gorm.DB { return d.DB.Clauses(dbresolver.Write) }

// Read 显式走只读副本(读写分离下)。无副本时等价于普通 DB。
func (d *DB) Read() *gorm.DB { return d.DB.Clauses(dbresolver.Read) }

// Close 关闭底层连接池(用于 app 优雅停机)。
func (d *DB) Close() error {
	sqlDB, err := d.DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// Ping 探测数据库连通性(可用于健康检查)。
func (d *DB) Ping(ctx context.Context) error {
	sqlDB, err := d.DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

// IsDuplicatedKey 判断是否唯一键冲突(gorm 语义错误或 MySQL 1062)。
// 开启 TranslateError(默认)后 gorm 会直接给 ErrDuplicatedKey;这里再兜底 MySQL 原生码。
func IsDuplicatedKey(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	var myErr *mysql.MySQLError
	if errors.As(err, &myErr) {
		return myErr.Number == 1062
	}
	return false
}

func (c Config) slowThreshold() time.Duration {
	if c.SlowThreshold > 0 {
		return c.SlowThreshold
	}
	return 200 * time.Millisecond
}

// ===== gorm logger → slog 桥 =====

type slogLogger struct {
	l     *slog.Logger
	level gormlogger.LogLevel
	slowT time.Duration
}

func newSlogLogger(l *slog.Logger, slow time.Duration) gormlogger.Interface {
	return &slogLogger{l: l, level: gormlogger.Warn, slowT: slow}
}

func (s *slogLogger) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	ns := *s
	ns.level = level
	return &ns
}

func (s *slogLogger) Info(ctx context.Context, msg string, data ...any) {
	if s.level >= gormlogger.Info {
		s.l.InfoContext(ctx, "gorm: "+msg, "data", data)
	}
}

func (s *slogLogger) Warn(ctx context.Context, msg string, data ...any) {
	if s.level >= gormlogger.Warn {
		s.l.WarnContext(ctx, "gorm: "+msg, "data", data)
	}
}

func (s *slogLogger) Error(ctx context.Context, msg string, data ...any) {
	if s.level >= gormlogger.Error {
		s.l.ErrorContext(ctx, "gorm: "+msg, "data", data)
	}
}

func (s *slogLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	if s.level <= gormlogger.Silent {
		return
	}
	elapsed := time.Since(begin)
	sql, rows := fc()
	switch {
	case err != nil && !errors.Is(err, gorm.ErrRecordNotFound):
		s.l.ErrorContext(ctx, "gorm: query", "err", err, "elapsed", elapsed, "rows", rows, "sql", sql)
	case s.slowT > 0 && elapsed > s.slowT:
		s.l.WarnContext(ctx, "gorm: slow query", "elapsed", elapsed, "threshold", s.slowT, "rows", rows, "sql", sql)
	case s.level >= gormlogger.Info:
		s.l.DebugContext(ctx, "gorm: query", "elapsed", elapsed, "rows", rows, "sql", sql)
	}
}
