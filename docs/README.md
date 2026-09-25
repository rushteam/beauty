# Beauty 文档索引 / Documentation Index

按主题列出每篇文档、它讲的包和可以直接运行的示例。第一次接触 Beauty 建议从
[Getting Started](getting-started.md) 开始，再看 [架构总览](architecture-diagram.md)。

> English: every topic has an English version in the **English** column. Package paths and
> examples are shared by both languages.

## 入门与架构

| 主题 | 中文 | English | 相关包 | 示例 |
|---|---|---|---|---|
| 15 分钟上手 | — | [getting-started](getting-started.md) | `beauty` | [complete](../examples/complete) |
| 架构总览 | [architecture-diagram](architecture-diagram.md) | [architecture-en](architecture-en.md) · [HTML](architecture.html) | `beauty` | — |
| 配置系统 | [configuration](configuration.md) | [configuration-en](configuration-en.md) | `pkg/conf` | [config](../examples/config) |
| 日志 | [logger](logger.md) | [logger-en](logger-en.md) | `pkg/service/logger` | — |
| 结构化错误码 | [error-codes](error-codes.md) | [error-codes-en](error-codes-en.md) | `pkg/api/errors` | [dberr](../examples/dberr) |
| 服务间上下文透传 | [metadata-propagation](metadata-propagation.md) | [metadata-propagation-en](metadata-propagation-en.md) | `pkg/api/metadata` | — |
| 可观测性(Trace / Metric / Grafana 看板) | [examples/observability](../examples/observability/README.md) | — | `pkg/service/telemetry` | [observability](../examples/observability) |

## CLI 与脚手架

| 主题 | 中文 | English | 相关包 | 示例 |
|---|---|---|---|---|
| `beauty` 工具链(new / add / api / dev / doctor) | [tools/README](../tools/README.md) | — | `tools/` | — |
| 统一模板 | [unified-template](unified-template.md) | [unified-template-en](unified-template-en.md) | `tools/tpls/unified` | — |
| API 命令 × Protobuf | [api-protobuf-integration](api-protobuf-integration.md) | [api-protobuf-integration-en](api-protobuf-integration-en.md) | `tools/internal/cmd/api` | [protobuf-example](../examples/protobuf-example) |
| 目录结构升级 | [directory-upgrade](directory-upgrade.md) · [upgrade-directory.sh](upgrade-directory.sh) | [directory-upgrade-en](directory-upgrade-en.md) | — | — |

## 服务发现与 RPC

| 主题 | 中文 | English | 相关包 | 示例 |
|---|---|---|---|---|
| gRPC 服务自动注册 | [grpc-service-discovery](grpc-service-discovery.md) | [grpc-service-discovery-en](grpc-service-discovery-en.md) | `pkg/service/grpcserver` · `pkg/service/discover` | [grpc-service-discovery](../examples/grpc-service-discovery) |
| gRPC 客户端服务发现 | [grpc-client-discovery](grpc-client-discovery.md) | [grpc-client-discovery-en](grpc-client-discovery-en.md) | `pkg/client/grpcclient` | [grpc-client-discovery](../examples/grpc-client-discovery) |
| gRPC DialContext 简化 API | [grpc-dial-context](grpc-dial-context.md) | [grpc-dial-context-en](grpc-dial-context-en.md) | `pkg/client/grpcclient` | [grpc-dial-context](../examples/grpc-dial-context) |
| gRPC 标签过滤器 | [grpc-label-filter](grpc-label-filter.md) | [grpc-label-filter-en](grpc-label-filter-en.md) | `pkg/client/grpcclient` | [grpc-label-filter](../examples/grpc-label-filter) |
| gRPC 标签选择器 | [grpc-label-selector](grpc-label-selector.md) | [grpc-label-selector-en](grpc-label-selector-en.md) | `pkg/client/grpcclient` | [grpc-label-selector](../examples/grpc-label-selector) |
| gRPC 注册中心插件 | [grpc-registry-plugin](grpc-registry-plugin.md) | [grpc-registry-plugin-en](grpc-registry-plugin-en.md) | `pkg/service/discover` | [grpc-registry-plugin](../examples/grpc-registry-plugin) · [simple](../examples/grpc-registry-plugin-simple) |
| HTTP 客户端服务发现 | [http-client-discovery](http-client-discovery.md) | [http-client-discovery-en](http-client-discovery-en.md) | `pkg/client/http` | [http-service-discovery](../examples/http-service-discovery) |
| 地域亲和路由 | [geo-routing](geo-routing.md) | [geo-routing-en](geo-routing-en.md) | `pkg/governance/router` | [geo-routing](../examples/geo-routing) |
| 跨框架互通 | [cross-service-interop](cross-service-interop.md) | [cross-service-interop-en](cross-service-interop-en.md) | `contrib/codec/*` | — |
| Kubernetes RBAC | [k8s-rbac](k8s-rbac.md) | [k8s-rbac-en](k8s-rbac-en.md) | `pkg/service/discover/k8s` · `pkg/store/dlock` | [services/k8s](../examples/services/k8s) |

## 中间件、安全与弹性

| 主题 | 中文 | English | 相关包 | 示例 |
|---|---|---|---|---|
| 中间件系统 | [middleware](middleware.md) | [middleware-en](middleware-en.md) | `pkg/middleware` | [security](../examples/security) |
| 中间件总览 | [middleware-summary](middleware-summary.md) | [middleware-summary-en](middleware-summary-en.md) | `pkg/middleware` | — |
| 内置 HTTP 中间件 | [middleware-builtin](middleware-builtin.md) | [middleware-builtin-en](middleware-builtin-en.md) | `pkg/middleware/*` | [api-security](../examples/api-security) |
| 认证与限流 | [auth-ratelimit](auth-ratelimit.md) | [auth-ratelimit-en](auth-ratelimit-en.md) | `pkg/middleware/auth` · `pkg/middleware/ratelimit` | [token](../examples/token) |
| 敏感词过滤(AC 自动机 + 中间件) | [sensitive-words](sensitive-words.md) | [sensitive-words-en](sensitive-words-en.md) | `pkg/foundation/actrie` · `pkg/middleware/sensitive` | [actrie](../examples/actrie) · [sensitive-middleware](../examples/sensitive-middleware) |
| 规则引擎 / 表达式求值 | [expr](expr.md) | [expr-en](expr-en.md) | `pkg/api/expr` | [expr](../examples/expr) |
| 分组限流 | [keyed-ratelimit](keyed-ratelimit.md) | [keyed-ratelimit-en](keyed-ratelimit-en.md) | `pkg/resilience/ratelimit` | [resilience](../examples/resilience) |
| DB 弹性(熔断 + 舱壁) | [db-resilience](db-resilience.md) | [db-resilience-en](db-resilience-en.md) | `contrib/sqldb` · `contrib/bun` · `contrib/gorm` | [dberr](../examples/dberr) |
| 幂等 | [idempotency](idempotency.md) | [idempotency-en](idempotency-en.md) | `pkg/store/idempotency` | [idempotency](../examples/idempotency) |

## 消息与任务编排

| 主题 | 中文 | English | 相关包 | 示例 |
|---|---|---|---|---|
| MQ 批处理 | [mq-batch](mq-batch.md) | [mq-batch-en](mq-batch-en.md) | `pkg/messaging/mq` | [mq](../examples/mq) |
| 任务队列 | [jobqueue](jobqueue.md) | [jobqueue-en](jobqueue-en.md) | `pkg/orchestration/jobqueue` | [delayqueue](../examples/delayqueue) |
| Redis 分布式任务队列 | [redisqueue](redisqueue.md) | [redisqueue-en](redisqueue-en.md) | `contrib/redisqueue` | — |
| DAG 执行器 | [dag](dag.md) | [dag-en](dag-en.md) | `pkg/foundation/dag` | [dag](../examples/dag) |

## 网关与 API 层

| 主题 | 中文 | English | 相关包 | 示例 |
|---|---|---|---|---|
| Higress 统一入口网关 | [higress-gateway](higress-gateway.md) | [higress-gateway-en](higress-gateway-en.md) | `tools/tpls/addons/higress` | [higress-gateway](../examples/higress-gateway) |
| Higress AI 网关 × LLM / MCP | [higress-ai-gateway](higress-ai-gateway.md) | [higress-ai-gateway-en](higress-ai-gateway-en.md) | `contrib/llm` · `contrib/mcp` | [higress-ai](../examples/higress-ai) |
| GraphQL / BFF | — | [graphql-bff](graphql-bff.md) | `contrib/graphql` | [graphql-bff](../examples/graphql-bff) |
| WASM Roadmap | [wasm-roadmap](wasm-roadmap.md) | [wasm-roadmap-en](wasm-roadmap-en.md) | `contrib/wasm*` | — |

## 实时通信与媒体

| 主题 | 中文 | English | 相关包 | 示例 |
|---|---|---|---|---|
| 实时服务组件库 | [realtime-components](realtime-components.md) | [realtime-components-en](realtime-components-en.md) | `pkg/transport` · `pkg/domain` | [chat](../examples/chat) |
| WebSocket | [websocket](websocket.md) | [websocket-en](websocket-en.md) | `pkg/transport/ws` | [websocket](../examples/websocket) |
| Server-Sent Events | [sse](sse.md) | [sse-en](sse-en.md) | `pkg/transport/sse` | [sse](../examples/sse) |
| P2P Transport 选型 | [p2p-transport](p2p-transport.md) | [p2p-transport-en](p2p-transport-en.md) | `pkg/transport/p2p` · `contrib/p2p-webrtc` | — |
| P2P 信令与组网 | [p2p-signaling](p2p-signaling.md) | [p2p-signaling-en](p2p-signaling-en.md) | `pkg/transport/p2p` | [p2p-signaling](../examples/p2p-signaling) |
| 媒体真机验证清单 | [media-validation](media-validation.md) | [media-validation-en](media-validation-en.md) | `pkg/media` | [hls](../examples/hls) · [webrtc-whip-whep](../examples/webrtc-whip-whep) |

## 游戏

| 主题 | 中文 | English | 相关包 | 示例 |
|---|---|---|---|---|
| 体素世界原语 | [voxel](voxel.md) | [voxel-en](voxel-en.md) | `pkg/game/voxel` | [voxel](../examples/voxel) |

## 基础数据结构

| 主题 | 中文 | English | 相关包 | 示例 |
|---|---|---|---|---|
| 过滤器 / 跳表 / 差异 | [data-structures](data-structures.md) | [data-structures-en](data-structures-en.md) | `pkg/foundation/filter` · `pkg/foundation/skiplist` · `pkg/foundation/diff` | [filter](../examples/filter) · [skiplist](../examples/skiplist) · [diff](../examples/diff) |

## 其他

| 主题 | 中文 | English |
|---|---|---|
| 生产案例模板 | — | [case-study-template](case-study-template.md) |
| contrib 模块一览 | [contrib/README](../contrib/README.md) | — |

新增文档时请同步更新本索引；中英文成对维护，英文文件以 `-en.md` 结尾。
