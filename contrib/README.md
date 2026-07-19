# contrib —— 可选集成(各自独立 Go 模块)

`contrib/` 下每个子目录是一个**独立的 Go 模块**(有自己的 `go.mod`,模块路径
`github.com/rushteam/beauty/contrib/<name>`),用来放**依赖较重**的第三方集成
(GORM、Elasticsearch、Kafka 等)。

## 为什么独立成模块

beauty 核心(`github.com/rushteam/beauty`)只保留轻量、通用的机制与接口。重依赖的具体实现
放进 contrib 独立模块,于是:

- **不用就零负担**:不 import 就不进你的依赖图——核心 `go.mod` 不会因为 gorm/ES 而变重。
- **可自己实现**:contrib 尽量面向核心的接口/约定编写(如 `pkg/mq` 的 `Publisher`、slog、
  OTel 全局 Provider),你完全可以照着自写一份、不用官方 contrib。
- **可钉不同版本**:各 contrib 独立打 tag、独立 `go get`,你能按需锁定版本,和核心解耦升级。

这与仓库里已有的 `tools/`(独立模块 `github.com/rushteam/beauty/tools`)是同一套路。

## 用法

```bash
go get github.com/rushteam/beauty/contrib/gorm@latest
```

各 contrib 在**自己的目录**里构建/测试(独立模块):

```bash
cd contrib/gorm && go test ./...
```

核心仓库的 `go build ./...` / `go test ./...` **不会**编译 contrib(模块边界隔离),
所以核心的依赖与 CI 不受 contrib 影响。

## 当前模块

| 模块 | 能力 | 主要依赖 |
|---|---|---|
| [`contrib/gorm`](gorm) | GORM 集成:读写分离(dbresolver)、otelgorm 链路、slog 日志桥、错误映射 | gorm.io/gorm、driver/mysql、otelgorm |
| [`contrib/sqldb`](sqldb) | database/sql 读写分离 + OTel(otelsql),配合 **sqlc**/sqlx/手写 SQL | XSAM/otelsql |
| [`contrib/nats`](nats) | `pkg/mq` 的 NATS broker 绑定(queue group 竞争 / 扇出;at-most-once) | nats.go |
| [`contrib/natsjs`](natsjs) | `pkg/mq` 的 NATS **JetStream** 绑定(持久化、at-least-once、重投、断线续) | nats.go/jetstream |
| [`contrib/kafka`](kafka) | `pkg/mq` 的 Kafka broker 绑定(consumer group;at-least-once,提交后确认) | segmentio/kafka-go |
| [`contrib/elasticsearch`](elasticsearch) | Elasticsearch 集成:健康 / 搜索 / 写入,暴露原始 JSON | go-elasticsearch/v8 |

`contrib/nats`、`contrib/natsjs`、`contrib/kafka` 实现核心 `pkg/mq` 的 `Publisher`/`Subscriber`
接口,故 `require github.com/rushteam/beauty`(已对齐发布版本,无 `replace`);`contrib/gorm`、
`contrib/sqldb`、`contrib/elasticsearch` 不依赖核心,可完全独立使用。

## 版本

各模块独立打 tag(`<模块目录>/vX.Y.Z`),独立 `go get`:

```bash
go get github.com/rushteam/beauty/contrib/gorm@v0.1.0
go get github.com/rushteam/beauty/contrib/sqldb@v0.1.0
go get github.com/rushteam/beauty/contrib/nats@v0.1.0    # 依赖核心 beauty v0.1.0
```

依赖核心的 mq 模块默认按 `require` 的核心版本解析。若本地要同时改核心与该 contrib,临时在其
`go.mod` 加一行 `replace github.com/rushteam/beauty => ../..` 即可(提交/发布前去掉)。
> 注:不建议用覆盖全仓的根 `go.work`——各 contrib 与核心的间接依赖版本可能不一致(如
> `genproto` 新旧拆分),合并工作区会触发 ambiguous import。按需只对"核心 + 单个 contrib"
> 做局部 replace 更稳。

## 约定

- contrib 模块**不得**被核心模块 import(否则依赖就漏进核心了)。
- 优先通过核心的**接口**(如 `mq.Publisher`/`mq.Subscriber`)或**标准约定**(`log/slog`、
  OTel 全局 Provider)与框架协作,尽量少直接依赖核心包;能脱离框架单用更好。
- 边界仍是"机制而非策略":contrib 负责把第三方库按 beauty 的可观测/配置约定接好,
  建模、迁移、业务逻辑留给使用方。
