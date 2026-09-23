# DB 弹性 — 熔断 + 舱壁 + 池饱和保护

针对 **InnoDB 行锁争用 → Error 1205 → 连接池耗尽 → 全站雪崩** 类故障,beauty 在 DB 访问层提供可选的降级保护。

> **定位**:防雪崩的**机制**,不是业务修复。热点行同步写仍需异步 / 批量 / 节流。

## ORM 选型（脚手架 / 新项目）

`beauty new` 生成的项目**默认不带 ORM**,持久层需自行接入 `contrib/`。若创建项目时**未指定** ORM,推荐优先级:

| 优先级 | 模块 | 适用场景 |
|--------|------|----------|
| **1（默认推荐）** | [`contrib/bun`](../contrib/bun) | 常规 CRUD、关系查询、需要较轻依赖的 ORM |
| 2 | [`contrib/gorm`](../contrib/gorm) | 团队已有 GORM 经验、生态插件 |
| 3 | [`contrib/sqldb`](../contrib/sqldb) | sqlc / sqlx / 手写 SQL、强类型查询生成 |

```bash
go get github.com/rushteam/beauty/contrib/bun@latest    # 未指定时优先
go get github.com/rushteam/beauty/contrib/gorm@latest   # 可选
go get github.com/rushteam/beauty/contrib/sqldb@latest  # sqlc 场景
```

> **Bun 注意**: `Update` 默认**跳过 Go 零值**(`0`、`""`、`false`、`nil` 等),无法把字段「更新为零值」。需显式 `Set("col = ?", val)` 或 `Column("col")` — 见 [contrib/bun/README.md](../contrib/bun/README.md#update-与零值)。

## 模块对照

| 场景 | 模块 | 底层 |
|------|------|------|
| sqlc / sqlx / 手写 SQL | `contrib/sqldb` | 原生 `database/sql` |
| GORM | `contrib/gorm` | ORM,底层仍是 `database/sql` |
| Bun | `contrib/bun` | ORM,底层仍是 `database/sql` |

核心逻辑在 `contrib/sqldb` 的 `Guard`,GORM 用 Plugin,Bun 用 QueryHook。

## 三层保护

1. **熔断** — 1205 / 1213 / deadline / 连接错误超阈 → `sqldb.ErrCircuitOpen`
2. **舱壁** — `MaxConcurrent` 限制同时在途 SQL(主要覆盖 Exec / Query)
3. **池饱和** — `InUse == MaxOpen` 且 `WaitCount` 增长 → `sqldb.ErrPoolSaturated`

**读写必须分开包装**,避免写热点把读路径一并熔断。

---

## sqldb (sqlc / sqlx)

```go
import (
    _ "github.com/go-sql-driver/mysql"
    "github.com/rushteam/beauty/contrib/sqldb"
    appdb "yourapp/db" // sqlc 生成
)

sdb, _ := sqldb.Open(sqldb.Config{
    Driver: "mysql", PrimaryDSN: "...", ReplicaDSNs: []string{"..."},
    MaxOpenConns: 50,
})
defer sdb.Close()

pool := sdb.Primary()

writeQ := appdb.New(sdb.ResilientWriter(sqldb.DefaultWriteResilience(pool)))
readQ  := appdb.New(sdb.ResilientReader(sqldb.DefaultReadResilience(pool)))

// 或手动包装
write := sqldb.WithResilience(sdb.Writer(), sqldb.PathResilience{
    Breaker:       &sqldb.BreakerSettings{Threshold: 0.3, MinRequests: 5},
    MaxConcurrent: 20,
    Pool:          pool,
})
```

### 自定义失败判定

```go
sqldb.PathResilience{
    IsFailure: sqldb.DefaultIsFailure, // 默认:1205/1213/deadline/连接
    // 或自定义:
    IsFailure: func(err error) bool {
        return sqldb.DefaultIsFailure(err) || errors.Is(err, myAppErr)
    },
}
```

### QueryRow 限制

标准库 `QueryRow` 在 `Scan` 时才发 SQL,`WithResilience` 对 QueryRow **仅做熔断 / 池检查**,不持有舱壁。

---

## GORM

```go
import (
    bgorm "github.com/rushteam/beauty/contrib/gorm"
    "github.com/rushteam/beauty/contrib/sqldb"
)

db, _ := bgorm.Open(bgorm.Config{DSN: "...", MaxOpenConns: 50})
defer db.Close()

sqlDB, _ := db.SQLDB()

write, _ := db.ResilientWrite(sqldb.DefaultWriteResilience(sqlDB))
read,  _ := db.ResilientRead(sqldb.DefaultReadResilience(sqlDB))

write.WithContext(ctx).Save(&u)   // 写 + 弹性
read.WithContext(ctx).First(&u, id) // 读 + 弹性
```

也可手动注册插件:

```go
_ = db.Write().Use(bgorm.NewResiliencePlugin(sqldb.DefaultWriteResilience(sqlDB)))
```

---

## Bun

```go
import (
    bbun "github.com/rushteam/beauty/contrib/bun"
    "github.com/rushteam/beauty/contrib/sqldb"
    _ "github.com/go-sql-driver/mysql"
)

db, _ := bbun.Open(bbun.Config{
    Driver: "mysql", DSN: "...", Replicas: []string{"..."},
    MaxOpenConns: 50,
})
defer db.Close()

write := db.ResilientWrite(sqldb.DefaultWriteResilience(db.Primary()))
read  := db.ResilientRead(sqldb.DefaultReadResilience(db.Primary()))

write.NewInsert().Model(&u).Exec(ctx)
read.NewSelect().Model(&u).Where("id = ?", id).Scan(ctx)
```

熔断打开时 Bun Hook 通过 `context.WithCancelCause` 快速失败,错误可能表现为 `context.Canceled`(cause 为 `sqldb.ErrCircuitOpen`)。

SQLite 测试:

```go
db, _ := bbun.OpenSQLite("file::memory:?cache=shared", bbun.Config{MaxOpenConns: 1})
```

---

## 配置参考

### PathResilience

| 字段 | 说明 |
|------|------|
| `Breaker` | 非 nil 启用熔断;nil 关闭 |
| `MaxConcurrent` | 舱壁并发上限;0 不限 |
| `MaxConcurrentWait` | 等舱壁时间;0 满则立即拒绝 |
| `Pool` | `*sql.DB`,用于池饱和检测 |
| `IsFailure` | 失败判定;nil 用 `DefaultIsFailure` |

### BreakerSettings 默认

| 路径 | Threshold | MinRequests | Cooldown |
|------|-----------|-------------|----------|
| 写 `DefaultWriteResilience` | 0.3 | 5 | 5s |
| 读 `DefaultReadResilience` | 0.6 | 10 | 3s |

### 错误码

| 错误 | 含义 |
|------|------|
| `sqldb.ErrCircuitOpen` | 熔断打开,快速失败 |
| `sqldb.ErrPoolSaturated` | 连接池饱和 |
| `sqldb.ErrConcurrencyLimited` | 舱壁已满 |

业务层可配合 `pkg/api/dberr` 映射为 503 / 504。

---

## 与业务修复的配合

| 层级 | 手段 |
|------|------|
| 业务 | 热点计数异步化、批量 flush、单行 UPDATE 节流 |
| 连接池 | 合理 `MaxOpenConns`;读写池拆分 |
| 框架 | 本弹性层 — 失败快速、限制放大 |

参考事故:每次浏览同步 `UPDATE template SET hot_score=...` → 应用层去掉同步写后恢复;弹性层可在类似场景下避免 8 Pod 同时池耗尽。
