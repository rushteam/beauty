# Changelog

本文件记录 Beauty 框架与配套工具链（`tools/`）的重要变更。

格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，
提交遵循 [约定式提交](https://www.conventionalcommits.org/zh-hans/)。
框架本身尚未打 tag，故以 `Unreleased` + 日期分组；`tools` 单独维护语义化版本
（见 [tools/README.md](tools/README.md) 的更新日志）。

## [Unreleased]

### Added
- **logger**：`pkg/service/logger` 新增 `NewTraceHandler`，包装任意 `slog.Handler`，
  在使用 `slog.*Context` 记录日志时自动注入当前 span 的 `trace_id` / `span_id`。
- **grpcclient**：客户端 xDS 支持——`xds:///service` 目标，opt-in 子包
  `pkg/client/grpcclient/xds`（空导入注册 resolver，`WithCredentials()` 提供 xDS mTLS）。
- **tools**：脚手架新增 `clean` 整洁架构模板（domain/application/adapter/infra/bootstrap
  四环 + user 示例），`beauty add handler|job` 增量生成器，`dev --watch` 热重载，
  `--module` / `--dry-run` / `--with-docker` / `--with-k8s` / `--with-ci` 选项，
  生成项目内置版本注入（`VERSION` + `-ldflags -X`）与统一 Makefile。

### Changed
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
