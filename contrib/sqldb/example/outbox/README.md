# outbox —— 事务性发件箱(示例,非库)

演示"改库 + 发消息"的**原子性**:在业务事务的同一个 `tx` 里顺带 `INSERT` 一行到 `outbox` 表
(同事务 → 原子),再由 relay 轮询发件箱投给消息系统、投成功即删。进程在任何点崩溃都不会出现
"库改了但消息没发"或"消息发了但库回滚"。

> 这是**应用层模式**,不是要引的库——`main.go` 里的 `enqueue` 与 `relayOnce` 两个函数就是全部实现
> (约 40 行),照抄进你的项目改即可。故意不做成 `pkg`/模块,避免为一个模式引入耦合。

## 运行

```bash
cd contrib/sqldb/example/outbox
go run .
```

输出(内存 sqlite,自包含):

```
① 转账 30(改库 + 入发件箱,同一事务):
   提交后:发件箱 1 条,余额 map[alice:70 bob:30]
② 转账失败(回滚):库不变,发件箱也不会多出消息:
   回滚后:发件箱仍 1 条,余额 map[alice:70 bob:30]   ← 原子性:回滚把发件箱那条也丢了
③ relay 投递发件箱:
  📤 发布 topic=transfer key=t1 payload={...}
   投出 1 条;剩余发件箱 0 条
```

## 耦合极薄

- **写入端**只用 `*sql.Tx.ExecContext`——`enqueue(ctx, tx, msg)` 用**你的事务**插一行。不绑 ORM/驱动:
  gorm 的 tx、`sqlx.Tx`、原生 `*sql.Tx`、`sqldb.Writer()` 都行。
- **投递端**只用 `*sql.DB` 查/删 + 一个 `Publisher` 接口(`Publish(ctx, Message)`)。不绑具体 broker。

## 接到真消息系统 / 生产化

- **换 Publisher**:把 demo 的 `logPublisher` 换成 `pkg/mq` / `contrib/nats` 的适配(3 行,见 `main.go` 注释)。
- **relay 放后台 + 只在 leader 上跑**:用 `pkg/dlock` 选主或 `pkg/service/cron`(leader-only),单实例即可,
  无需 `SELECT ... FOR UPDATE SKIP LOCKED`;多副本并发拉取再加 SKIP LOCKED(Postgres/MySQL8)。
- **at-least-once**:投成功但删除前崩溃 → 下次重投 → 消费端需幂等(见 `pkg/idempotency`)。
- **DLQ / 最大重试 / 按 key 有序 / 定期清理**:按需自行加,不在本示例内。

## 表结构

```sql
CREATE TABLE outbox (
  id         BIGINT PRIMARY KEY AUTO_INCREMENT,
  topic      VARCHAR(255) NOT NULL,
  msg_key    VARCHAR(255),
  payload    BLOB NOT NULL,
  headers    JSON,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```
迁移用你自己的工具;Postgres 把占位符换成 `$1..$n`、`BLOB`→`BYTEA`、`JSON`→`JSONB`。
