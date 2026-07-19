# sqlc + contrib/sqldb 端到端样例

演示在**读写分离**下用 [sqlc](https://sqlc.dev) 生成的类型安全查询:sqlc 的 `Queries` 吃 `DBTX`,
[`contrib/sqldb`](..) 的 `Writer()`/`Reader()`/`RW()` 正是 `DBTX`,直接对接。

> 样例放在 `contrib/sqldb` 模块内(而非核心 `examples/`)——核心是独立模块、不能 import contrib;
> 样例与它演示的模块同模块最省事(无需 replace)。

## 运行

```bash
cd contrib/sqldb/example
go run .
```

输出:

```
写(Writer):创建 #1 Alice
写(RW 自动识别 RETURNING→主库):创建 #2 Bob
读(Reader,共 2 位):
  #1 Alice  bio="poet"
  #2 Bob    bio=""
读己之写(RW + Primary(ctx)→主库):#1 Alice
```

为可运行(免真库),主库与只读副本都指向**同一个 sqlite 文件**;真实部署把
`Config.ReplicaDSNs` 填成独立从库地址即可,**代码不变**。

## 文件

| 文件 | 作用 |
|---|---|
| `schema.sql` | 表结构(sqlc 据此推断类型) |
| `query.sql` | 查询(`-- name: Xxx :one/:many/:exec`) |
| `sqlc.yaml` | sqlc 配置(engine: sqlite) |
| `appdb/` | **sqlc 生成**的代码(已提交,便于免装 sqlc 直接跑) |
| `main.go` | 用 `contrib/sqldb` 接生成代码,演示三种路由 |

## 重新生成 appdb

改了 `schema.sql` / `query.sql` 后,装 [sqlc](https://docs.sqlc.dev/en/latest/overview/install.html) 再:

```bash
cd contrib/sqldb/example
sqlc generate   # 按 sqlc.yaml 重新生成 appdb/
```

## 三种路由用法(见 main.go)

- **显式**(正确、推荐):`appdb.New(db.Writer())` 写、`appdb.New(db.Reader())` 读。
- **自动路由**:`appdb.New(db.RW())`——`CreateAuthor`(`:one` + `RETURNING`,走 QueryRow 却是写)
  被 SQL 意图嗅探自动路由到主库;纯 `SELECT` 走副本。
- **读己之写**:`q.GetAuthor(sqldb.Primary(ctx), id)` 强制走主库。
