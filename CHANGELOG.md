# Changelog

本文件记录 Beauty 框架与配套工具链（`tools/`）的重要变更。

格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，
提交遵循 [约定式提交](https://www.conventionalcommits.org/zh-hans/)。
框架本身尚未打 tag，故以 `Unreleased` + 日期分组；`tools` 单独维护语义化版本
（见 [tools/README.md](tools/README.md) 的更新日志）。

## [Unreleased]

### Added
- **dlock**：新增 URL/DSN 工厂，与 `conf.New` 的 scheme 工厂模式对齐——
  `dlock.New(dsn)` 构造 `Locker`、`dlock.NewElector(dsn)` 构造 `Elector`，空导入对应
  infra 子包即注册（`etcd`/`etcdv3`、`consul`、`redis` 注册两者；`k8s` 只注册 Elector）。
  cron 的 `WithLeaderElector` 可直接吃 DSN 构造出的 elector。
- **kvstore**：`pkg/infra/etcd` 新增 `kvstore.Store` 的 etcd 实现（lease 做 TTL、
  可精确查剩余；事务/CAS 做 SetNX/Incr）。与 redis 实现互补——etcd 秒级 TTL、
  已有 etcd 免再引 Redis；redis 毫秒级 TTL、`Incr` 原生原子。
- **docs**：新增 `pkg/infra/README.md`——基建适配层总览（能力矩阵 + 各后端
  conf/dlock/kvstore/discover 的 DSN 与最小用法）。
- **dlock**：分布式锁/选主后端补齐两家——
  - **consul**（`pkg/infra/consul`，零新依赖）：基于 session + KV Acquire 实现
    `Locker`（含真非阻塞 `TryLock`）+ `Elector`，`RenewPeriodic` 续期,`NewDLock` /
    `NewDLockFromConfig` 构造;
  - **redis**（`pkg/infra/redis`，新增 `go-redis/v9` 依赖）：单节点 SET NX PX +
    Lua CAS 释放/续期实现 `Locker` + `Elector`(如实标注非 Redlock 的单节点语义)。
- **kvstore**：`pkg/infra/redis` 新增 `kvstore.Store` 的 Redis 实现,给
  counter/cooldown/idempotency 等原语一个真正跨实例共享的后端。
- **conf**：新增基于 k8s ConfigMap / Secret 的配置中心后端（opt-in 子包
  `pkg/infra/k8s`，空导入注册 `configmap` / `secret` scheme）。纯 k8s 部署无需额外
  运维 nacos/etcd 即可热更新配置——`conf.New("configmap://<ns>/<name>/<dataKey>")`，
  按 `metadata.name` watch 目标资源、断线指数退避重连、按值去重避免无谓重载。
- **logger**：`pkg/service/logger` 新增 `NewTraceHandler`，包装任意 `slog.Handler`，
  在使用 `slog.*Context` 记录日志时自动注入当前 span 的 `trace_id` / `span_id`。
- **grpcclient**：客户端 xDS 支持——`xds:///service` 目标，opt-in 子包
  `pkg/client/grpcclient/xds`（空导入注册 resolver，`WithCredentials()` 提供 xDS mTLS）。
- **tools**：脚手架新增 `clean` 整洁架构模板（domain/application/adapter/infra/bootstrap
  四环 + user 示例），`beauty add handler|job` 增量生成器，`dev --watch` 热重载，
  `--module` / `--dry-run` / `--with-docker` / `--with-k8s` / `--with-ci` 选项，
  生成项目内置版本注入（`VERSION` + `-ldflags -X`）与统一 Makefile。

### Changed
- **infra/discover**：收敛各后端的 client 构造——`pkg/infra/{etcd,consul,k8s}` 各暴露
  `NewClient` / `NewClientset`，`pkg/service/discover/{etcdv3,consul,k8s}` 改为复用
  （消除与配置中心/分布式锁重复的连接构造，对齐 nacos 早已复用 `infra/nacos` 的做法）。
- **dlock/k8s**：`pkg/infra/k8s` 新增 `NewElectorFromConfig`（从 kubeconfig/集群内配置
  直接建 clientset），对齐 `etcd.NewDLockFromConfig` 的便捷构造。
- **tools**：升级到 `tools` v0.1.0；生成的 `go.mod` 升级 `go 1.26` 与当前 beauty 版本；
  模板 logger 默认接入 `NewTraceHandler`，生成项目日志自动带 trace 关联。

### Fixed
- **tools**：修复模板与框架 API 漂移导致生成项目无法编译的问题（中间件接口、
  注册中心字段、cron 模板坏死包、unified 服务组合裁剪等）。详见
  [tools/README.md](tools/README.md) 的 v0.1.0 条目。

> 注：使用 `tools` 生成依赖上述 `logger.NewTraceHandler` 的项目时，
> 需待该框架变更发布后（脚手架会执行 `go get github.com/rushteam/beauty@latest`）方可拉取到。

## 近期亮点（按提交）

- `feat(config)`：`WithConfig` 配置热重载与示例。
- `feat(webhook)`：事件驱动的 webhook 通知器。
- `feat(featureflag)`：本地特性开关与 A/B 实验。
- `feat(vars)`：`${KEY}` / `${KEY:-default}` 配置插值。
- `feat(middleware/callbacks)`：将 callbacks 切面接入 http/grpc。
- `feat(sse,ws)`：基于 `stream.Broadcaster` 的广播辅助。
- `feat(options)`：分层函数式选项。
- `feat(stream)`：面向多订阅者的扇出广播器。
