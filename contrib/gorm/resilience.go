package gorm

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"

	sqldb "github.com/rushteam/beauty/contrib/sqldb"
	"gorm.io/gorm"
)

const resilienceSessionKey = "beauty/gorm/resilience/session"

var resiliencePluginSeq atomic.Uint64

// ResiliencePlugin 在 GORM 回调链中接入 sqldb.Guard(熔断 + 舱壁 + 池饱和)。
type ResiliencePlugin struct {
	guard *sqldb.Guard
	name  string
}

// NewResiliencePlugin 创建 GORM 弹性插件。
func NewResiliencePlugin(cfg sqldb.PathResilience) *ResiliencePlugin {
	id := resiliencePluginSeq.Add(1)
	return &ResiliencePlugin{
		guard: sqldb.NewGuard(cfg),
		name:  fmt.Sprintf("beauty:resilience:%d", id),
	}
}

func (p *ResiliencePlugin) Name() string { return p.name }

func (p *ResiliencePlugin) Initialize(db *gorm.DB) error {
	cb := db.Callback()
	for _, pair := range []struct {
		before func(string, func(*gorm.DB)) error
		after  func(string, func(*gorm.DB)) error
	}{
		{cb.Create().Before("*").Register, cb.Create().After("*").Register},
		{cb.Query().Before("*").Register, cb.Query().After("*").Register},
		{cb.Update().Before("*").Register, cb.Update().After("*").Register},
		{cb.Delete().Before("*").Register, cb.Delete().After("*").Register},
		{cb.Row().Before("*").Register, cb.Row().After("*").Register},
		{cb.Raw().Before("*").Register, cb.Raw().After("*").Register},
	} {
		if err := pair.before(p.name+":before", p.before); err != nil {
			return err
		}
		if err := pair.after(p.name+":after", p.after); err != nil {
			return err
		}
	}
	return nil
}

const resilienceAdmittedKey = "beauty/gorm/resilience/admitted"

func (p *ResiliencePlugin) owns(db *gorm.DB) bool {
	v, _ := db.Get(resilienceSessionKey)
	return v == p.name
}

func (p *ResiliencePlugin) before(db *gorm.DB) {
	if !p.owns(db) {
		return
	}
	ctx := db.Statement.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err := p.guard.Admit(ctx); err != nil {
		_ = db.AddError(err)
		return
	}
	db.InstanceSet(resilienceAdmittedKey, true)
}

func (p *ResiliencePlugin) after(db *gorm.DB) {
	if !p.owns(db) {
		return
	}
	if _, ok := db.InstanceGet(resilienceAdmittedKey); !ok {
		return
	}
	p.guard.Finish(db.Error)
}

// ResilientWrite 返回带写路径弹性保护的 GORM 会话。
func (d *DB) ResilientWrite(cfg sqldb.PathResilience) (*gorm.DB, error) {
	return d.applyResilience(d.Write(), cfg)
}

// ResilientRead 返回带读路径弹性保护的 GORM 会话。
func (d *DB) ResilientRead(cfg sqldb.PathResilience) (*gorm.DB, error) {
	return d.applyResilience(d.Read(), cfg)
}

func (d *DB) applyResilience(gdb *gorm.DB, cfg sqldb.PathResilience) (*gorm.DB, error) {
	if cfg.Pool == nil {
		sqlDB, err := d.DB.DB()
		if err != nil {
			return nil, err
		}
		cfg.Pool = sqlDB
	}
	plugin := NewResiliencePlugin(cfg)
	sess := gdb.Session(&gorm.Session{})
	if err := sess.Use(plugin); err != nil {
		return nil, err
	}
	return sess.Set(resilienceSessionKey, plugin.name), nil
}

// SQLDB 返回底层连接池(配置 DefaultWrite/DefaultRead 时用)。
func (d *DB) SQLDB() (*sql.DB, error) {
	return d.DB.DB()
}
