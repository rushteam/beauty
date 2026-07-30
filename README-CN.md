<div align="center">

# Beauty

**一套生命周期跑微服务 —— 顺带实时媒体，以及 WASM · Agent 沙箱**

HTTP · gRPC · 定时 · 房间 · 流媒体，都挂在同一个 `app.Start(ctx)`。
需要直播链路或 AI/策略插件时，还是这套核心来宿主。

[![Go Reference](https://pkg.go.dev/badge/github.com/rushteam/beauty.svg)](https://pkg.go.dev/github.com/rushteam/beauty)
[![Go Report Card](https://goreportcard.com/badge/github.com/rushteam/beauty)](https://goreportcard.com/report/github.com/rushteam/beauty)
![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

[English](README.md) · **中文**

</div>

---

Beauty 有一个很小的核心(`beauty.New(...).Start(ctx)`),把任意多个服务放在同一套优雅停机的
生命周期下运行——**机制而非策略**:每个包只解决一个问题、不侵入你的业务。依赖较重或可选的栈
(GORM、Kafka、LLM、WASM…)以**独立模块**放在 [`contrib/`](contrib) 下,用什么才引什么。

选 Beauty 的三个理由:

| 主线 | 你得到什么 |
|---|---|
| **统一生命周期** | HTTP、gRPC(含网关)、定时任务、MQ 消费者、任意自定义 `Service`,共用一次 `Start` / 优雅停机。 |
| **实时 + 媒体** | WebSocket / SSE / 扇出、游戏循环 + AOI/在线;以及 RTMP → HLS / LL-HLS、WebRTC WHIP/WHEP + SFU——都是 Service,不是另一套栈。 |
| **WASM · Agent** | wazero 沙箱跑 HTTP 过滤器、FaaS 函数、OPA/Rego 鉴权、LLM agent 工具/技能——纯 Go、无 CGo。 |

## 亮点

- **统一生命周期**:一个 `app.Start(ctx)` 管 HTTP、gRPC、定时任务和任意 `Service`;配置/发现/韧性/可观测内建。
- **实时 + 媒体**:WS/SSE/QUIC、定步长游戏循环、空间 AOI 与在线;RTMP 采集、HLS / LL-HLS origin、WebRTC WHIP/WHEP + SFU、多路流管理。
- **WASM · Agent**:[`contrib/wasm`](contrib/wasm)(中间件 / FaaS-lite 路由)、[`contrib/wasmopa`](contrib/wasmopa)(Rego→wasm 鉴权)、[`contrib/wasmagent`](contrib/wasmagent)(沙箱 agent 工具);LLM / RAG / MCP 见 [`contrib/llm`](contrib/llm) · [`contrib/vector`](contrib/vector) · [`contrib/mcp`](contrib/mcp)。路线图:[`docs/wasm-roadmap.md`](docs/wasm-roadmap.md)。
- **其余能力**:配置热更新(nacos/etcd/consul/k8s)、服务发现、分布式锁/选主、限流/熔断/过载保护、传输无关 MQ、OpenTelemetry、一致性哈希分片,以及 contrib 里的数据/搜索/broker 模块。

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

## 微服务：注册 · 发现 · 调用

上面是「同进程多 Service」;跨进程才是微服务主路径。Beauty 用注册中心(etcd / nacos / …)
把服务挂出去,调用方按名字拨号——负载均衡、标签路由、重试都在客户端里。

**提供方**——启动时注册到 etcd:

```go
registry := etcdv3.NewRegistry(&etcdv3.Config{
	Endpoints: []string{"127.0.0.1:2379"},
	Prefix:    "/beauty",
	TTL:       10,
})

app := beauty.New(
	beauty.WithRegistry(registry),
	beauty.WithService(grpcserver.New(":9090",
		func(s *grpc.Server) {
			pb.RegisterGreeterServer(s, &greeter{})
		},
		grpcserver.WithServiceName("helloworld.rpc"),
		grpcserver.WithMetadata(map[string]string{"env": "production"}),
	)),
)
app.Start(ctx)
```

**调用方**——按服务名发现并调用(可在另一个进程 / 另一个服务里):

```go
conn, err := grpcclient.DialContext(ctx, "beauty://helloworld.rpc?env=production",
	grpcclient.WithRegistry(registry),
	grpcclient.WithLoadBalancer("p2c_ewma"),
)
if err != nil {
	return err
}
defer conn.Close()

client := pb.NewGreeterClient(conn)
resp, err := client.SayHello(ctx, &pb.HelloRequest{Name: "beauty"})
```

也支持直接写注册中心地址,不必先构造 `Registry`:

```go
// etcd://host:port/serviceName  或  nacos://host:port/serviceName?...
conn, err := grpcclient.DialContext(ctx, "nacos://127.0.0.1:8848/helloworld.rpc")
```

| 能力 | 说明 |
|---|---|
| 注册 | `beauty.WithRegistry` / `grpcserver.WithAutoServiceDiscovery` |
| 拨号 | `grpcclient.DialContext`(`beauty://` · `etcd://` · `nacos://`) |
| 路由 | query 标签过滤(`env`/`region`/…)、加权 / P2C 负载均衡 |
| HTTP | 对称 API 见 `pkg/client/http` |

更多见 [`docs/grpc-service-discovery.md`](docs/grpc-service-discovery.md)、[`docs/grpc-dial-context.md`](docs/grpc-dial-context.md),
以及示例 [`examples/grpc-service-discovery`](examples/grpc-service-discovery)、[`examples/grpc-dial-context`](examples/grpc-dial-context)。

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
| WASM / Agent | `contrib/wasm`(中间件 + FaaS)、`contrib/wasmopa`(OPA/Rego)、`contrib/wasmagent`(agent 工具/技能);见 [`docs/wasm-roadmap.md`](docs/wasm-roadmap.md) |
| 消息 | `pkg/mq`、`pkg/eventbus`、`pkg/webhook`、`pkg/delayqueue`、`pkg/scheduler` |
| 一致性 | `pkg/saga`、`pkg/txn`、`pkg/idempotency` |
| 可观测 | `pkg/service/telemetry`、`pkg/service/logger`、`pkg/buildinfo`、`pkg/service/pprof` |
| 横向扩展 | `pkg/shard`(一致性哈希路由 + 反向代理) |
| 鉴权 | `pkg/middleware/auth`(认证)、`pkg/authz`(授权:RBAC + HTTP/gRPC 中间件)、`pkg/token` |
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
| [`contrib/wasm`](contrib/wasm) | wazero 运行时:HTTP 中间件、FaaS-lite 路由、host funcs、池/缓存 | `…/contrib/wasm` |
| [`contrib/wasmopa`](contrib/wasmopa) | OPA Rego→wasm 策略,实现 `pkg/authz.Enforcer` | `…/contrib/wasmopa` |
| [`contrib/wasmagent`](contrib/wasmagent) | 沙箱 agent 工具 / 技能(`ScriptExecutor` + `agent.Tool`) | `…/contrib/wasmagent` |

路径前缀均为 `github.com/rushteam/beauty`。详见 [`contrib/README.md`](contrib/README.md)。

## 可观测

OpenTelemetry 贯穿框架:trace 与 metrics 走 `pkg/service/telemetry`,日志走 `pkg/service/logger`
(slog,自动注入 `trace_id`/`span_id`),运行时构建信息用 `pkg/buildinfo`。配好一次导出器,
媒体/mq/客户端各层就会自动上报指标。

## 文档

- [`docs/`](docs) —— 配置、中间件、服务发现、日志、实时组件等。
- [`docs/wasm-roadmap.md`](docs/wasm-roadmap.md) —— WASM 分层(运行时、agent、OPA、FaaS)。
- [`examples/`](examples) —— 大部分功能的可运行示例。
- [`CHANGELOG.md`](CHANGELOG.md) —— 重要变更。
- [`docs/media-validation.md`](docs/media-validation.md) —— 媒体链路真机验证清单。

## 贡献

欢迎 Issue 与 PR。提交前请跑 `go test ./...`(以及相关 `contrib/<模块>` 的测试)与 `gofmt`。

## 许可

MIT —— 见 [LICENSE](LICENSE)。
