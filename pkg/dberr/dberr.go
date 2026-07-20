// Package dberr 把数据库驱动错误翻译为 pkg/errors 的 *Status,
// 让仓储层只抛原生 driver error,中间件/网关层统一拿到带业务码的错误。
//
// 错误翻译层:
//   - 驱动层只产 error,业务层调 Translate(err) 得到带 Code 的 *Status;
//   - 翻译分两步:Driver.Classify(err) → ErrClass(枚举),再按表映射到 Code;
//   - ErrClass 是 DB 无关的稳定分类(唯一约束、外键、可空、死锁、超时、连接断开...),
//     各 driver 适配器各自实现 Classify,业务层只认 ErrClass。
//
// 零值不可用,用 New 构造。Translator 与各 Driver 适配器并发安全(只读映射)。
package dberr

import (
	stderrors "errors"

	perr "github.com/rushteam/beauty/pkg/errors"
)

// ErrClass 数据库错误的稳定分类(DB 无关)。
type ErrClass int

const (
	ClassUnknown             ErrClass = iota // 未能识别
	ClassUniqueViolation                     // 唯一约束冲突(重复键)
	ClassForeignKeyViolation                 // 外键约束冲突
	ClassNotNullViolation                    // 非空约束冲突
	ClassCheckViolation                      // check 约束冲突
	ClassDeadlock                            // 死锁/序列化失败
	ClassTimeout                             // 查询超时
	ClassConnection                          // 连接断开/不可达
	ClassNotFound                            // 行不存在(影响 0 行的 NoRows)
	ClassTooManyRows                         // 期望单行却返回多行
)

// Driver 把底层 driver error 归类为 ErrClass。
// 实现方对接具体驱动(pgx/mysql/go-sql-driver/sqlite...),只暴露这一个方法。
type Driver interface {
	Classify(err error) ErrClass
}

// Translator 把 ErrClass 翻译为 *pkg/errors.Status。
type Translator struct {
	driver Driver
	table  map[ErrClass]perr.Code
}

// Option 配置 Translator。
type Option func(*config)

type config struct {
	driver Driver
	table  map[ErrClass]perr.Code
}

// WithDriver 指定驱动适配器。必填。
func WithDriver(d Driver) Option { return func(c *config) { c.driver = d } }

// WithMapping 覆盖某 ErrClass → Code 的映射(可多次调用)。
func WithMapping(class ErrClass, code perr.Code) Option {
	return func(c *config) { c.table[class] = code }
}

// New 创建 Translator。driver 为空时所有错误都归为 ClassUnknown。
func New(opts ...Option) *Translator {
	cfg := &config{
		table: defaultTable(),
	}
	for _, o := range opts {
		o(cfg)
	}
	return &Translator{driver: cfg.driver, table: cfg.table}
}

// Translate 把底层 error 翻译为 *Status。
// err 已是 *errors.Status 时原样返回;无 driver 时回退到 CodeInternal。
func (t *Translator) Translate(err error) *perr.Status {
	if err == nil {
		return nil
	}
	if s, ok := perr.FromError(err); ok {
		return s
	}
	class := ClassUnknown
	if t.driver != nil {
		class = t.driver.Classify(err)
	}
	code, ok := t.table[class]
	if !ok {
		code = t.table[ClassUnknown]
	}
	return perr.New(code, err.Error()).WithCause(err)
}

// Class 返回 err 的 ErrClass(经 driver 归类)。便于业务层做条件分支。
func (t *Translator) Class(err error) ErrClass {
	if t.driver == nil {
		return ClassUnknown
	}
	return t.driver.Classify(err)
}

// Is 判断 err 是否属于某 ErrClass(经 driver 归类)。
func (t *Translator) Is(err error, class ErrClass) bool {
	return t.Class(err) == class
}

// defaultTable 默认 ErrClass → Code 映射(可被 WithMapping 覆盖)。
// 语义:冲突→409、不存在→404、超时→504、连接→503。
func defaultTable() map[ErrClass]perr.Code {
	return map[ErrClass]perr.Code{
		ClassUnknown:             perr.CodeInternal,
		ClassUniqueViolation:     perr.CodeConflict,
		ClassForeignKeyViolation: perr.CodeConflict,
		ClassNotNullViolation:    perr.CodeInvalidArgument,
		ClassCheckViolation:      perr.CodeInvalidArgument,
		ClassDeadlock:            perr.CodeConflict,
		ClassTimeout:             perr.CodeDeadline,
		ClassConnection:          perr.CodeUnavailable,
		ClassNotFound:            perr.CodeNotFound,
		ClassTooManyRows:         perr.CodeInternal,
	}
}

// ---- 通用 driver 适配 ----

// NoopDriver 把所有错误归为 ClassUnknown。无 DB 依赖时的占位。
type NoopDriver struct{}

func (NoopDriver) Classify(error) ErrClass { return ClassUnknown }

// ErrorIsDriver 用 errors.Is 做归类:适合标准库 database/sql 的 ErrBadConn/ErrTainted
// 以及 driver 自身导出的哨兵 error。每条规则按顺序匹配,命中即返回。
type ErrorIsDriver struct {
	Rules []ErrorIsRule
}

// ErrorIsRule 一条 errors.Is 归类规则。
type ErrorIsRule struct {
	Target error
	Class  ErrClass
}

// Classify 按规则顺序匹配第一个命中的 errors.Is。
func (d ErrorIsDriver) Classify(err error) ErrClass {
	for _, r := range d.Rules {
		if r.Target != nil && stderrors.Is(err, r.Target) {
			return r.Class
		}
	}
	return ClassUnknown
}
