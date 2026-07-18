# contrib/gorm —— beauty 的 GORM 集成(独立模块)

把 GORM 按 beauty 的可观测/配置约定接好:读写分离、OTel 链路、slog 日志桥、连接池、
错误映射。**独立 Go 模块**,不进 beauty 核心依赖图;不 import 核心,只用标准库 `slog`
(beauty 的 logger 会喂进来)与 OTel 全局 Provider(beauty 的 telemetry 会配好),
因此也能脱离框架单用。

```bash
go get github.com/rushteam/beauty/contrib/gorm@latest
```

## 用法

```go
import bgorm "github.com/rushteam/beauty/contrib/gorm"

db, err := bgorm.Open(bgorm.Config{
    DSN:          "user:pass@tcp(127.0.0.1:3306)/app?parseTime=true",
    Replicas:     []string{"user:pass@tcp(replica:3306)/app?parseTime=true"}, // 读写分离,可空
    MaxOpenConns: 50,
    MaxIdleConns: 10,
    // DisablePrepareStmt / DisableTracing / SkipTranslateError 都默认关(即默认开启对应能力)
})
if err != nil { panic(err) }
defer db.Close() // 挂到 app 优雅停机

// db 嵌入 *gorm.DB,GORM 全部方法直接用:
db.WithContext(ctx).Create(&u)      // 默认走主库
db.Read().First(&u, id)             // 显式走只读副本
db.Write().Save(&u)                 // 显式走主库

if bgorm.IsDuplicatedKey(err) { /* 唯一键冲突 */ }
```

非 MySQL(Postgres/SQLite/测试)用 `OpenWith` 传任意 `gorm.Dialector`:

```go
db, _ := bgorm.OpenWith(postgres.Open(dsn), nil, bgorm.Config{})
```

## 能力

- **读写分离**:`Config.Replicas` 非空即用 `dbresolver`(主写、从随机读);`Write()`/`Read()` 手动切。
- **链路追踪**:默认接 `otelgorm`(用 beauty telemetry 配好的全局 Provider;未配则 no-op)。`DisableTracing` 关。
- **日志**:gorm 日志桥到 `slog`(默认 `slog.Default()`,`WithLogger` 指定);慢查询(默认 >200ms)告警、错误 error、其余 debug。
- **错误翻译**:默认开 `TranslateError`,配合 `IsDuplicatedKey` 判唯一键冲突(gorm 语义错误 + MySQL 1062 兜底)。
- **连接池**:`MaxOpenConns`/`MaxIdleConns`/`ConnMaxLifetime`(默认 1h)/`ConnMaxIdleTime`。
- **健康检查**:`Ping(ctx)`。

## 边界

建模、迁移、仓储模式、事务编排都在使用方——本模块只负责把 GORM 接好。要 Outbox(可靠"改库+
发消息")可配合 `pkg/mq`,自行在同一事务里写 outbox 表。
