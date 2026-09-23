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

## 边界

建模、迁移、查询在使用方;驱动由使用方空导入(`mysql` / `pgx` / `modernc.org/sqlite`)。
