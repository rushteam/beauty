# contrib/sqldb —— database/sql 读写分离 + OTel(独立模块)

给 `database/sql` 提供**主从读写分离**与 **OTel 埋点**。和 **sqlc** 生成的代码天然配合(sqlc 的
`Queries` 接受 `DBTX` 接口,本模块的 `Writer()`/`Reader()` 正是 `DBTX`),也可用于 sqlx / 手写 SQL。
独立模块,不 import beauty 核心。

```bash
go get github.com/rushteam/beauty/contrib/sqldb@latest
```

> 可运行样例(sqlite 免真库):[`example/`](example)(sqlc + 读写分离)、
> [`example/outbox/`](example/outbox)(事务性发件箱模式,"改库+发消息"原子性)。

## 配合 sqlc(推荐:显式读写句柄)

```go
import (
    _ "github.com/go-sql-driver/mysql" // 自行空导入驱动
    "github.com/rushteam/beauty/contrib/sqldb"
    "yourapp/db" // sqlc 生成的包,含 db.New(DBTX) *Queries
)

sdb, _ := sqldb.Open(sqldb.Config{
    Driver:      "mysql",
    PrimaryDSN:  "user:pass@tcp(primary:3306)/app?parseTime=true",
    ReplicaDSNs: []string{"user:pass@tcp(replica:3306)/app?parseTime=true"}, // 可多个,轮询
    MaxOpenConns: 50,
})
defer sdb.Close()

readQ  := db.New(sdb.Reader()) // 读走副本
writeQ := db.New(sdb.Writer()) // 写走主库

u, _ := readQ.GetUser(ctx, id)
_    = writeQ.CreateUser(ctx, params)

// 事务(在主库上开):
tx, _ := sdb.Primary().BeginTx(ctx, nil)
q := writeQ.WithTx(tx)
// ... q.XxxContext(ctx, ...)
tx.Commit()
```

## 自动路由(带 SQL 意图嗅探)

`sdb.RW()` 返回一个自动路由的 `DBTX`:`Exec`→主库;`Query`/`QueryRow` 先按 **SQL 意图**判定——
含**写意图**(写动词开头、`RETURNING`、`FOR UPDATE`/`FOR SHARE`、数据修改 CTE)自动走主库,否则走副本。
传给 `db.New(sdb.RW())` 即可"一个 Queries 走两边":

```go
q := db.New(sdb.RW())
q.GetUser(ctx, id)                 // 纯 SELECT → 副本
q.GetUserForUpdate(ctx, id)        // SELECT ... FOR UPDATE → 自动走主库
row, _ := q.InsertReturning(ctx, p)// INSERT ... RETURNING → 自动走主库
q.GetUser(sqldb.Primary(ctx), id)  // 读己之写:显式强制主库
```

嗅探是**保守启发式**:拿不准偏向主库(牺牲读分流、保正确;"写→副本"是危险失败,"读→主库"
只是少分流)。它能挡住 `RETURNING`/`FOR UPDATE` 这类常见坑,但 SQL 启发式**无法 100% 覆盖**
所有写法(藏在字符串/注释里的关键字会误判为主库——方向安全)。**需要确定性保证时,用显式
`Writer()`/`Reader()`**,那是唯一的 correct-by-construction 路径。

## 能力

- **读写分离**:`Writer()`(主)/`Reader()`(副本轮询,无副本回退主)/`Primary()`(拿 *sql.DB 开事务/迁移)。
- **OTel**:默认经 `XSAM/otelsql` 埋点——命令级 trace + DB stats metrics(用 beauty telemetry 的全局
  Provider,未配 no-op)。`DisableTracing`/`DisableMetrics` 关。
- **连接池**:`MaxOpenConns`/`MaxIdleConns`/`ConnMaxLifetime`(默认 1h)/`ConnMaxIdleTime`。
- **健康**:`Ping(ctx)`(探主 + 所有副本)。

## DB 弹性(熔断 + 舱壁)

> 完整文档:[docs/db-resilience.md](../../docs/db-resilience.md)(含 GORM / Bun 用法)。

针对 **锁等待超时(1205) → 连接池耗尽 → 全站雪崩** 类故障,可在 DBTX 层套可选保护。
**读写应分别包装**,避免写热点误伤读路径:

```go
writeQ := db.New(db.ResilientWriter(sqldb.DefaultWriteResilience(db.Primary())))
readQ  := db.New(db.ResilientReader(sqldb.DefaultReadResilience(db.Primary())))

// 或手动:
write := sqldb.WithResilience(db.Writer(), sqldb.PathResilience{
    Breaker: &sqldb.BreakerSettings{Threshold: 0.3, MinRequests: 5},
    MaxConcurrent: 20, // 写并发舱壁(可选)
    Pool: db.Primary(), // 池满 + WaitCount 增长时快速失败
})
```

能力:

- **熔断**:1205/1213/deadline/连接错误计入失败率,超阈快速返回 `ErrCircuitOpen`
- **舱壁**:`MaxConcurrent` 限制同时在途 SQL(主要覆盖 Exec/Query)
- **池饱和**:`InUse == MaxOpen` 且等待数增长时返回 `ErrPoolSaturated`

`QueryRow` 因标准库在 `Scan` 才发 SQL,仅做熔断/池检查(不做舱壁持有)。
根因(热点行同步写)仍需业务层异步/批量/节流。

## 边界

不 import 数据库驱动(使用方空导入 mysql/pgx/sqlite);建模、迁移、查询 SQL(交给 sqlc)在使用方。
`.sql` → Go 代码用 sqlc CLI 生成(`sqlc generate`),本模块只提供其运行时所需的读写分离 DBTX。
端到端(真库 + 主从复制)请在具备环境验证;单测用两个内存 sqlite 库验证路由落点。
