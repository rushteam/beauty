<div align="center">

# Beauty

**开箱即用的 Go 微服务框架**

用一套生命周期编排 HTTP · gRPC · 定时 · 实时 · 媒体等服务 ——
内建配置、发现、韧性、消息与可观测。

[![Go Reference](https://pkg.go.dev/badge/github.com/rushteam/beauty.svg)](https://pkg.go.dev/github.com/rushteam/beauty)
[![Go Report Card](https://goreportcard.com/badge/github.com/rushteam/beauty)](https://goreportcard.com/report/github.com/rushteam/beauty)
![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

[English](README.md) · **中文**

</div>

---

Beauty 有一个很小的核心(`beauty.New(...).Start(ctx)`),把任意多个服务放在同一套优雅停机的
生命周期下运行;并提供一大批**机制而非策略**的能力包——每个包只解决一个问题、不侵入你的业务。
依赖较重或可选的集成(GORM、Kafka、LLM…)以**独立模块**放在 [`contrib/`](contrib) 下,用什么才引什么。

## 亮点

- **统一服务**:HTTP、gRPC(含网关)、定时任务,以及任意自定义 `Service`,都在一个 `app.Start(ctx)` 下优雅停机。
- **配置与发现**:配置中心热更新(nacos/etcd/consul/k8s)、服务发现、分布式锁/选主、带 TTL 的 KV。
- **韧性**:限流、熔断、过载保护、退避;重试 + 熔断已接进 HTTP/gRPC 客户端。
- **实时**:WebSocket / SSE / 扇出广播、QUIC 传输、定步长游戏循环、空间 AOI 与在线状态。
- **媒体**:RTMP 采集、HLS / LL-HLS origin、WebRTC WHIP/WHEP 与 SFU 会议室、多路流管理。
- **消息**:传输无关的消息队列抽象 +「消费者即 Service」,broker 绑定在 contrib。
- **可观测**:OpenTelemetry trace 与 metrics、带 trace 关联的 slog 日志、构建信息、pprof。
- **横向扩展**:一致性哈希分片,把有状态服务(房间/流/会话)路由到多副本。
- **contrib 模块**:数据(GORM、database/sql + sqlc)、搜索、MQ broker(NATS/JetStream/Kafka)、以及 **AI**(LLM 客户端、向量/RAG、MCP)。

## 安装

```bash
# 库
go get github.com/rushteam/beauty

# CLI(脚手架、代码生成、开发热重载)
go install github.com/rushteam/beauty/tools/cmd/beauty@latest
```

## 快速开始

最小服务:

```go
package main

import (
	"context"
	"net/http"

	"github.com/rushteam/beauty"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello from beauty"))
	})

	app := beauty.New(
		beauty.WithWebServer(":8080", mux, beauty.WithServiceName("hello")),
	)
	if err := app.Start(context.Background()); err != nil {
		panic(err)
	}
}
```

用 CLI 生成完整项目:

```bash
beauty new my-service   # 生成项目(目录、Makefile,可选 Docker/k8s)
cd my-service && go run .
```

## 组合服务

`beauty.New` 接受任意多个服务;每个实现极小的 `Service` 接口(`Start(ctx) error` + `String() string`),
一起优雅停机:

```go
app := beauty.New(
	beauty.WithWebServer(":8080", mux, beauty.WithServiceName("api")),
	beauty.WithGrpcServer(":9090", func(s *grpc.Server) {
		pb.RegisterGreeterServer(s, &greeter{})
	}, beauty.WithServiceName("grpc")),
	beauty.WithService(myCustomService), // 任意带 Start/String 的对象
)
app.Start(ctx) // 阻塞到收到信号;各服务一并停机
```

- **HTTP**:任意 `http.Handler`(chi/gin/net-http)。
- **gRPC**:注册你的 server;内建标准 health service 与重试策略。REST 网关见 `pkg/service/grpcgw`。
- **定时任务**:仅在选主 leader 上运行的周期任务。

## 能力总览

| 领域 | 包 |
|---|---|
| 配置 / 热更新 | `pkg/conf`(nacos、etcd、consul、k8s configmap/secret) |
| 服务发现 | `pkg/service/discover`,客户端 `pkg/client/{grpcclient,http}` |
| 分布式锁 / 选主 | `pkg/dlock`(etcd、consul、redis、k8s) |
| TTL-KV 与原语 | `pkg/kvstore`(redis、etcd)→ counter / cooldown / idempotency |
| 并发 | `pkg/syncx`(Map/ForEach、SingleFlight、Batcher、Debounce/Throttle、Future)、`pkg/xgo`、`pkg/safe`、`pkg/chanx`、`pkg/keyedmutex` |
| 韧性 | `pkg/ratelimit`、`pkg/governance/{circuitbreaker,overloadctrl}`、`pkg/backoff` |
| 实时 | `pkg/ws`、`pkg/sse`、`pkg/stream`、`pkg/quic`、`pkg/gameloop`、`pkg/spatial`、`pkg/presence` |
| 媒体 | `pkg/media/rtmp`、`pkg/hls`、`pkg/media/hlsmux`、`pkg/media/webrtc`(含 `sfu`)、`pkg/media`(hub/supervisor/metrics) |
| 消息 | `pkg/mq`、`pkg/eventbus`、`pkg/webhook`、`pkg/delayqueue`、`pkg/scheduler` |
| 一致性 | `pkg/saga`、`pkg/txn`、`pkg/idempotency` |
| 可观测 | `pkg/service/telemetry`、`pkg/service/logger`、`pkg/buildinfo`、`pkg/service/pprof` |
| 横向扩展 | `pkg/shard`(一致性哈希路由 + 反向代理) |
| 鉴权 | `pkg/middleware/auth`、`pkg/token` |
| 领域 / 游戏 | `pkg/{leaderboard,matchmaker,leveling,questlog,versus,tally,reddot,...}` |

细节见 [`docs/`](docs) 与可运行示例 [`examples/`](examples)。

## 消息

传输无关的队列(`pkg/mq`):`Publisher`/`Subscriber` 接口 +「消费者即 `beauty.Service`」的
`Consumer`,外加 `Chain`/`Retry`/`Recover` 中间件。核心自带进程内实现;真实 broker 是 contrib 可选模块。

```go
consumer := mq.NewConsumer(broker).
	Handle("orders", handle, mq.WithGroup("order-workers"))
app := beauty.New(beauty.WithService(consumer))
```

## contrib 模块

依赖较重 / 可选的集成是**独立 Go 模块**(各自 `go.mod`、独立打 tag)——按需引入,核心依赖图保持精简。

| 模块 | 能力 | `go get` |
|---|---|---|
| [`contrib/gorm`](contrib/gorm) | GORM:读写分离、otel 链路、slog、错误映射 | `…/contrib/gorm` |
| [`contrib/sqldb`](contrib/sqldb) | `database/sql` 读写分离 + otel,配合 **sqlc** | `…/contrib/sqldb` |
| [`contrib/elasticsearch`](contrib/elasticsearch) | Elasticsearch v8 搜索 / 写入 / 健康 | `…/contrib/elasticsearch` |
| [`contrib/nats`](contrib/nats) | `pkg/mq` 的 NATS broker(at-most-once) | `…/contrib/nats` |
| [`contrib/natsjs`](contrib/natsjs) | `pkg/mq` 的 NATS JetStream(持久、at-least-once) | `…/contrib/natsjs` |
| [`contrib/kafka`](contrib/kafka) | `pkg/mq` 的 Kafka broker(consumer group) | `…/contrib/kafka` |
| [`contrib/llm`](contrib/llm) | provider 无关 LLM 客户端(对话/流式/embedding,OpenAI/Anthropic/Azure/兼容) | `…/contrib/llm` |
| [`contrib/vector`](contrib/vector) | 向量存储 / RAG 语义检索 | `…/contrib/vector` |
| [`contrib/mcp`](contrib/mcp) | Model Context Protocol server/client(把服务暴露成 AI 工具) | `…/contrib/mcp` |

路径前缀均为 `github.com/rushteam/beauty`。详见 [`contrib/README.md`](contrib/README.md)。

## 可观测

OpenTelemetry 贯穿框架:trace 与 metrics 走 `pkg/service/telemetry`,日志走 `pkg/service/logger`
(slog,自动注入 `trace_id`/`span_id`),运行时构建信息用 `pkg/buildinfo`。配好一次导出器,
媒体/mq/客户端各层就会自动上报指标。

## 文档

- [`docs/`](docs) —— 配置、中间件、服务发现、日志、实时组件等。
- [`examples/`](examples) —— 大部分功能的可运行示例。
- [`CHANGELOG.md`](CHANGELOG.md) —— 重要变更。
- [`docs/media-validation.md`](docs/media-validation.md) —— 媒体链路真机验证清单。

## 贡献

欢迎 Issue 与 PR。提交前请跑 `go test ./...`(以及相关 `contrib/<模块>` 的测试)与 `gofmt`。

## 许可

MIT —— 见 [LICENSE](LICENSE)。
