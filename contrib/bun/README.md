# contrib/bun —— beauty 的 Bun ORM 集成(独立模块)

把 [Bun](https://bun.uptrace.dev/) 按 beauty 约定接好:读写句柄、连接池、**DB 弹性**(复用 `contrib/sqldb.Guard`)。
独立 Go 模块,不进 beauty 核心依赖图。

```bash
go get github.com/rushteam/beauty/contrib/bun@latest
```

> 完整弹性文档:[docs/db-resilience.md](../../docs/db-resilience.md)

## 用法

```go
import (
    _ "github.com/go-sql-driver/mysql"
    bbun "github.com/rushteam/beauty/contrib/bun"
    "github.com/rushteam/beauty/contrib/sqldb"
)

db, _ := bbun.Open(bbun.Config{
    Driver: "mysql",
    DSN:    "user:pass@tcp(primary:3306)/app?parseTime=true",
    Replicas: []string{"user:pass@tcp(replica:3306)/app?parseTime=true"},
    MaxOpenConns: 50,
})
defer db.Close()

write := db.ResilientWrite(sqldb.DefaultWriteResilience(db.Primary()))
read  := db.ResilientRead(sqldb.DefaultReadResilience(db.Primary()))

write.NewInsert().Model(&u).Exec(ctx)
read.NewSelect().Model(&u).Where("id = ?", id).Scan(ctx)
```

SQLite(测试):

```go
db, _ := bbun.OpenSQLite("file::memory:?cache=shared", bbun.Config{MaxOpenConns: 1})
```

## 能力

- **读写句柄**:`Write()` 主库 / `Read()` 副本(无副本回退主库)
- **连接池**:`MaxOpenConns` / `MaxIdleConns` / `ConnMaxLifetime`(默认 1h)
- **弹性**:`ResilientWrite` / `ResilientRead` — Bun `QueryHook` + `sqldb.Guard`
- **方言**:`mysql` / `pgx` / `postgres` / `sqlite`(默认)

## Update 与零值

Bun 的 `Update().Model(&m)` 与 GORM 类似:**默认忽略 struct 中的零值字段**,不会生成 `SET col = 0 / '' / false`。
若业务需要「把字段清空/归零」,必须显式指定列,否则 silent skip:

```go
// ❌ count 为 0 时不会写入数据库
u := User{ID: 1, Count: 0}
db.NewUpdate().Model(&u).WherePK().Exec(ctx)

// ✅ 显式 Set,零值也会更新
db.NewUpdate().Model(&u).Where("id = ?", u.ID).
    Set("count = ?", u.Count).
    Exec(ctx)

// ✅ 或 Column 限定要更新的列(含零值)
db.NewUpdate().Model(&u).Column("count", "status").WherePK().Exec(ctx)
```

常见踩坑:软删除恢复(`deleted_at = NULL`)、计数归零、状态置 `false`、字符串置空 — 都要用 `Set` / `Column`,不要只依赖 `Model` 整体 diff。

## 脚手架默认选型

`beauty new` **不内置 ORM**;若创建项目时未指定持久层方案,文档与团队约定**优先 Bun**(`contrib/bun`),其次 GORM,sqlc 场景用 sqldb。见 [docs/db-resilience.md](../../docs/db-resilience.md#orm-选型脚手架--新项目)。

## 边界

建模、迁移、查询在使用方;驱动由使用方空导入(`mysql` / `pgx` / `modernc.org/sqlite`)。
