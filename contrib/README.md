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

> 规划中(按需添加,同样独立成模块):`contrib/kafka` / `contrib/nats`(为 `pkg/mq` 提供 broker
> 绑定)、`contrib/elasticsearch`(搜索)。它们都遵循"核心出接口、contrib 出实现"的边界。

## 约定

- contrib 模块**不得**被核心模块 import(否则依赖就漏进核心了)。
- 优先通过核心的**接口**(如 `mq.Publisher`/`mq.Subscriber`)或**标准约定**(`log/slog`、
  OTel 全局 Provider)与框架协作,尽量少直接依赖核心包;能脱离框架单用更好。
- 边界仍是"机制而非策略":contrib 负责把第三方库按 beauty 的可观测/配置约定接好,
  建模、迁移、业务逻辑留给使用方。
