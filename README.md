# Beauty Framework

Beauty is a Go-based microservices framework designed to simplify the development and deployment of microservices. It provides a unified way to create and manage different types of services including HTTP servers, gRPC servers, and cron jobs.

## Prerequisites

Before using the Beauty framework, ensure you have the following installed:

- Go 1.18 or later
- [buf](https://buf.build/) for Protocol Buffers (for gRPC services)

```bash
# Install buf on macOS
brew install bufbuild/buf/buf

# Install buf on Linux
# See https://buf.build/docs/installation for more options
```

## Installation

```bash
# Clone the repository
git clone https://github.com/rushteam/beauty.git

# Install the Beauty CLI tool
cd beauty/tools
go install ./cmd/beauty
```

## Creating a New Project

You can create a new project using the Beauty CLI tool:

```bash
# Create a new project
beauty new my-service
```

The CLI will prompt you to enter:
- Project module name (Go module path)
- Web framework choice (chi or gin)

### 快速开始

```bash
# 创建新项目
beauty new my-service

# 进入项目目录
cd my-service

# 安装依赖
go mod tidy

# 生成 gRPC 代码（如果有 .proto 文件）
make generate

# 运行项目
go run main.go
```

## Project Structure

Beauty 框架采用统一的项目结构，支持 HTTP、gRPC 和 Cron 三种服务类型：

```
my-service/
├── api/                    # Protocol Buffers 定义
│   ├── user.proto
│   └── v1/
│       ├── user.pb.go
│       ├── user_grpc.pb.go
│       └── user.pb.gw.go
├── buf.gen.yaml           # buf 代码生成配置
├── buf.yaml               # buf 项目配置
├── config/                # 配置文件
│   └── dev/
│       └── app.yaml
├── go.mod
├── main.go                # 应用入口
├── Makefile               # 构建脚本
├── scripts/               # 工具脚本
│   └── generate.sh
└── internal/              # 内部代码
    ├── config/            # 配置管理
    │   └── config.go
    ├── endpoint/          # 服务端点
    │   ├── grpc/          # gRPC 服务
    │   │   └── server.go
    │   ├── handlers/      # HTTP 处理器
    │   │   ├── user.go
    │   │   └── health.go
    │   ├── job/           # 定时任务
    │   │   └── cron.go
    │   └── router/        # HTTP 路由
    │       ├── http.go
    │       ├── middleware.go
    │       └── router.go
    └── infra/             # 基础设施
        ├── conf/          # 配置加载
        │   └── conf.go
        ├── logger/        # 日志
        │   └── logger.go
        ├── middleware/    # 中间件
        │   └── middleware.go
        └── registry/      # 服务注册
            └── registry.go
```

**核心特性**：
- **多协议支持**: HTTP、gRPC、Cron 三种服务类型
- **代码生成**: 基于 Protocol Buffers 自动生成 gRPC 代码
- **服务发现**: 支持多种注册中心（etcd / nacos / consul / polaris / k8s），gRPC 与 HTTP 客户端均内置服务发现 + 负载均衡（轮询 / 加权轮询 / 随机）+ 重试换节点
- **配置中心**: 统一 `conf.New(url)` 接入本地文件与远程配置，支持热加载
- **中间件**: recovery、cors、compress、health、auth、限流、熔断、超时
- **实时推送**: 内置 SSE（`pkg/sse`）与 WebSocket（`pkg/ws`）封装，开箱即用
- **实时服务原语**: 44 个可独立组合的实时组件（会话/在场/路由/匹配/排名/调度/钱包/审计/通知/事务/Saga/负载均衡/幂等/延迟队列/状态机/ID 生成/直播 PK/计数聚合/寻路/细粒度锁/连击热度/空间索引/退避重试/事件总线/地理编码等），纯标准库实现，详见下文 [Realtime Components](#realtime-components)
- **流程编排**: 轻量 DAG 执行器（`pkg/dag`），拓扑分层 + 层内并行
- **可观测性**: 内置 OpenTelemetry 链路追踪与指标收集（HTTP/gRPC 请求指标由 otelhttp/otelgrpc 自动采集）
- **动态日志**: 运行时通过 HTTP 接口调整日志级别，无需重启

## Configuration

The framework uses YAML configuration files. The default configuration file is located at `config/config.yaml`:

```yaml
app: my-service
http:
  addr: :8080
log:
  level: info
  format: text
```

## Creating Services

Beauty supports multiple types of services:

### HTTP Server

```go
import (
    "net/http"
    "github.com/rushteam/beauty"
    "github.com/go-chi/chi/v5"
)

func main() {
    r := chi.NewRouter()
    r.Get("/", func(w http.ResponseWriter, r *http.Request) {
        w.Write([]byte("Hello, World!"))
    })

    app := beauty.New(
        beauty.WithWebServer(
            ":8080",
            r,
            beauty.WithServiceName("my-http-service"),
        ),
    )

    if err := app.Start(context.Background()); err != nil {
        log.Fatalln(err)
    }
}
```

### gRPC Server

```go
import (
    "context"
    "github.com/rushteam/beauty"
    "google.golang.org/grpc"
    pb "your-project/api/v1"
)

type GreeterServer struct {
    pb.UnimplementedGreeterServer
}

func (s *GreeterServer) SayHello(ctx context.Context, req *pb.HelloRequest) (*pb.HelloReply, error) {
    return &pb.HelloReply{Message: "Hello " + req.Name}, nil
}

func main() {
    app := beauty.New(
        beauty.WithGrpcServer(
            ":50051",
            func(s *grpc.Server) {
                pb.RegisterGreeterServer(s, &GreeterServer{})
            },
            beauty.WithServiceName("my-grpc-service"),
        ),
    )

    if err := app.Start(context.Background()); err != nil {
        log.Fatalln(err)
    }
}
```

### Cron Jobs

```go
import (
    "context"
    "github.com/rushteam/beauty"
    "github.com/rushteam/beauty/pkg/service/cron"
)

func main() {
    app := beauty.New(
        beauty.WithCrontab(
            cron.WithJob("@every 1m", func(ctx context.Context) error {
                // Your job logic here
                return nil
            }),
        ),
    )

    if err := app.Start(context.Background()); err != nil {
        log.Fatalln(err)
    }
}
```

## Service Registration and Discovery

Beauty supports service registration and discovery using etcd or nacos:

```go
import (
    "github.com/rushteam/beauty"
    "github.com/rushteam/beauty/pkg/service/discover/etcdv3"
)

func main() {
    app := beauty.New(
        // Your services here...
        beauty.WithRegistry(etcdv3.NewRegistry(&etcdv3.Config{
            Endpoints: []string{"127.0.0.1:2379"},
            Prefix: "/beauty",
        })),
    )

    if err := app.Start(context.Background()); err != nil {
        log.Fatalln(err)
    }
}
```

### Using DSN

```go
// etcd DSN
a, _ := grpcclient.New(
    context.Background(),
    grpcclient.WithAddr("etcd://user:pass@127.0.0.1:2379/beauty?ttl=10&dial_ms=3000#helloworld.rpc"),
)

// nacos DSN (supports cluster/group/namespace/app_name/weight)
a, _ := grpcclient.New(
    context.Background(),
    grpcclient.WithAddr("nacos://127.0.0.1:8848/helloworld.rpc?app_name=test&cluster=DEFAULT&group=DEFAULT_GROUP"),
)
```

## Tracing and Metrics

Beauty provides built-in support for OpenTelemetry tracing and metrics:

```go
import (
    "github.com/rushteam/beauty"
    "github.com/rushteam/beauty/pkg/service/telemetry"
    "go.opentelemetry.io/otel/exporters/prometheus"
)

func main() {
    metricExporter, err := prometheus.New()
    if err != nil {
        panic(err)
    }

    app := beauty.New(
        // Your services here...
        beauty.WithTrace(), // Enable tracing
        beauty.WithMetric(telemetry.WithMetricReader(metricExporter)), // Enable metrics
    )

    if err := app.Start(context.Background()); err != nil {
        log.Fatalln(err)
    }
}
```

HTTP/gRPC 请求指标由内置的 otelhttp/otelgrpc instrumentation 自动产出，Go runtime 指标（goroutine、GC、heap）默认开启（`telemetry.WithoutMetricRuntime()` 可关闭）。生产环境通常用 OTLP 出口：

```go
beauty.WithTrace(telemetry.WithTraceOTLPGRPCExporter()),   // OTLP/gRPC，默认读 OTEL_EXPORTER_OTLP_* 环境变量
beauty.WithMetric(telemetry.WithMetricOTLPGRPCReader()),   // 指标周期上报到 Collector
```

**Exemplars**（指标↔链路关联）默认按 `trace_based` 启用——在已采样的 trace 上下文中产生的指标会带上 trace_id，可在 Grafana 从延迟直方图直接跳到对应 trace。OTLP exporter 默认导出 exemplar；Prometheus 需以 OpenMetrics 格式抓取。可用 `telemetry.WithMetricExemplarFilter(exemplar.AlwaysOffFilter)` 关闭，或通过环境变量 `OTEL_METRICS_EXEMPLAR_FILTER` 配置。

## Running Multiple Services

Beauty allows you to run multiple services in a single application:

```go
func main() {
    app := beauty.New(
        beauty.WithWebServer(":8080", httpHandler, beauty.WithServiceName("web-service")),
        beauty.WithGrpcServer(":50051", grpcRegistrar, beauty.WithServiceName("grpc-service")),
        beauty.WithCrontab(cronOptions...),
    )

    if err := app.Start(context.Background()); err != nil {
        log.Fatalln(err)
    }
}
```

## Lifecycle Hooks

You can add hooks to be executed at different stages of the application lifecycle:

```go
app := beauty.New(
    // Your services here...
)

// Add a hook to be executed before the application starts
app.Hook(beauty.EventBeforeRun, func(app *beauty.App) {
    // Your code here
})

// Add a hook to be executed after the application stops
app.Hook(beauty.EventAfterRun, func(app *beauty.App) {
    // Your cleanup code here
})
```

## Complete Example

Here's a complete example that combines multiple services:

```go
package main

import (
    "context"
    "log"
    "net/http"

    "github.com/go-chi/chi/v5"
    "github.com/rushteam/beauty"
    "github.com/rushteam/beauty/pkg/service/discover/etcdv3"
    "github.com/rushteam/beauty/pkg/service/telemetry"
    "google.golang.org/grpc"
    pb "your-project/api/v1"
)

type GreeterServer struct {
    pb.UnimplementedGreeterServer
}

func (s *GreeterServer) SayHello(ctx context.Context, req *pb.HelloRequest) (*pb.HelloReply, error) {
    return &pb.HelloReply{Message: "Hello " + req.Name}, nil
}

func main() {
    // HTTP Router
    r := chi.NewRouter()
    r.Get("/", func(w http.ResponseWriter, r *http.Request) {
        w.Write([]byte("Hello, World!"))
    })

    app := beauty.New(
        // HTTP Service
        beauty.WithWebServer(
            ":8080",
            r,
            beauty.WithServiceName("my-http-service"),
        ),
        
        // gRPC Service
        beauty.WithGrpcServer(
            ":50051",
            func(s *grpc.Server) {
                pb.RegisterGreeterServer(s, &GreeterServer{})
            },
            beauty.WithServiceName("my-grpc-service"),
        ),
        
        // Service Registry
        beauty.WithRegistry(etcdv3.NewRegistry(&etcdv3.Config{
            Endpoints: []string{"127.0.0.1:2379"},
            Prefix: "/beauty",
        })),
        
        // Tracing
        beauty.WithTrace(),
    )

    if err := app.Start(context.Background()); err != nil {
        log.Fatalln(err)
    }
}
```

## Configuration Center

Beauty 支持统一的配置加载接口，通过 URL scheme 切换本地文件与远程配置中心：

```go
import (
    "github.com/rushteam/beauty/pkg/conf"
    _ "github.com/rushteam/beauty/pkg/infra/etcd"   // 注册 etcd scheme
    _ "github.com/rushteam/beauty/pkg/infra/nacos"  // 注册 nacos scheme
)

// 本地文件
loader, _ := conf.New("config/app.yaml")

// etcd 远程配置
loader, _ := conf.New("etcd://127.0.0.1:2379/myapp/config.yaml")

// nacos 远程配置
loader, _ := conf.New("nacos://127.0.0.1:8848/myapp.yaml?namespace=dev")

var cfg AppConfig
loader.Unmarshal(&cfg)

// 热加载
loader.Watch(ctx, func() {
    loader.Unmarshal(&cfg)
})
```

支持 etcd / nacos / consul / polaris，详见 [docs/configuration.md](docs/configuration.md)。

## Dynamic Log Level

运行时通过 HTTP 接口动态调整日志级别，无需重启：

```go
import "github.com/rushteam/beauty/pkg/service/logger"

// 挂载管理接口
mux.Handle("/debug/loglevel", logger.LevelHandler())

// 代码方式
logger.SetLevelByName("debug")
```

```bash
# 查询当前级别
curl http://localhost:8080/debug/loglevel

# 临时开启 debug
curl -X PUT http://localhost:8080/debug/loglevel -d '{"level":"debug"}'
```

详见 [docs/logger.md](docs/logger.md)。

## Built-in Middleware

| 中间件 | 包 | 说明 |
|--------|-----|------|
| Recovery | `middleware/recovery` | panic 捕获，返回 500，支持自定义上报 |
| CORS | `middleware/cors` | 跨域处理，支持细粒度配置 |
| Compress | `middleware/compress` | gzip 响应压缩，按 minSize 控制 |
| Health | `middleware/health` | `/healthz` 存活 + `/readyz` 就绪探针 |
| Auth | `middleware/auth` | JWT/静态 Token 认证，可扩展 |
| RateLimit | `middleware/ratelimit` | 令牌桶限流，支持按 IP/用户/自定义键 |
| CircuitBreaker | `middleware/circuitbreaker` | 三态熔断器 |
| Timeout | `middleware/timeout` | 请求超时 + 慢请求检测 |

详见 [docs/middleware-builtin.md](docs/middleware-builtin.md) 和 [docs/middleware-summary.md](docs/middleware-summary.md)。

## HTTP Server Options

```go
beauty.WithWebServer(":8080", handler,
    webserver.WithServiceName("api"),
    webserver.WithReadTimeout(30 * time.Second),
    webserver.WithWriteTimeout(30 * time.Second),
    webserver.WithIdleTimeout(90 * time.Second),
    webserver.WithShutdownTimeout(30 * time.Second),
    webserver.WithTLS("cert.pem", "key.pem"),   // 启用 HTTPS
    webserver.WithMiddleware(myMiddleware),
)
```

## gRPC Server Options

```go
beauty.WithGrpcServer(":9090", register,
    grpcserver.WithServiceName("rpc"),
    grpcserver.WithGrpcServerUnaryInterceptor(myInterceptor),
    grpcserver.WithGracefulStopTimeout(10 * time.Second),
    grpcserver.WithTLS("cert.pem", "key.pem"),
    // Keepalive 参数已内置合理默认值
)
```

## Realtime Components

beauty 在 `pkg/ws` / `pkg/sse` 之上提供一组**可独立组合**的实时服务原语，覆盖长连接会话、在线状态、消息路由、匹配组队、排行榜、任务调度、虚拟账户、操作审计、离线通知、周期榜单、临时小队、版本化存储、社交图谱、频道历史、会话令牌、DB 错误翻译、可靠 Webhook、断线重连、状态广播、短期 TTL KV、跨域事务等典型场景。均为纯标准库实现，遵循 beauty 风格（泛型 + 函数式 Option + 中文 doc）。

包按"通用 vs 业务"分两个命名空间：

- **`pkg/`** — 通用实时原语（不预设业务语义）
- **`pkg/domain/`** — 业务实体（预设了业务模型：货币 / 通知 / 小队 / 赛季榜 / 存档 / 社交 / 频道）

### 通用原语（pkg/）

| 包 | 一句话 | 典型场景 |
|----|--------|----------|
| `pkg/match` | 有状态实时会话原语（actor 模型） | 游戏房间 / 权威对战 / 协作编辑 |
| `pkg/ws/session` | WebSocket 有状态会话高阶封装 | 长连接业务 / IM 单聊 |
| `pkg/presence` | 在线状态双索引 + 事件总线 | 频道成员 / 在线广播 / 候选池 |
| `pkg/router` | 多语义消息路由 + 攒批 | 群发 / 定点投递 / 批量下发 |
| `pkg/leaderboard` | 排行榜内存排名缓存（堆排序） | "我的名次" / TopN 高频读 |
| `pkg/scheduler` | 工作池 + 运行时 Pause/Resume | 发奖 / 批量通知 / 过期清理 |
| `pkg/matchmaker` | 基于属性匹配的组队 | PVP 组队 / 匹配大厅 |
| `pkg/audit` | 操作审计（仅记成功 + 异步落盘） | 合规 / 运维审计 |
| `pkg/token` | dual token（JWT HS256）+ 黑名单注销 | 登录态签发 / 续签 / 踢出 |
| `pkg/dberr` | DB 错误码翻译（DB-agnostic → *Status） | 仓储层错误归一为业务码 |
| `pkg/webhook` | 事件通知 + 幂等去重 + DLQ | 外部系统回调 / at-least-once |
| `pkg/resume` | 断线重连在场还原（token + presence） | 掉线不掉状态 / 自动重连 |
| `pkg/presence/status` | 状态变化广播给关注者 | 好友上下线通知 / status event |
| `pkg/ephemeral` | 短期 TTL KV（纯内存 + 过期清扫） | 验证码 / 临时数据 / 缓存 |
| `pkg/afterwork` | 请求级后台任务延寿（waitUntil 语义） | 响应后发邮件 / 写审计 / 触发 webhook |
| `pkg/handler` | 声明式 HTTP handler 包装器 | 业务函数只写 `(ctx,req)=>(resp,err)` |
| `pkg/ratelimit` | 按键限流（令牌桶 + 滑动窗口）+ HTTP 中间件 | 防刷屏 / API 限流 / 按用户/IP 隔离 |
| `pkg/txn` | 跨域事务协调（两阶段提交 Prepare/Commit/Rollback） | 扣钱包+写存档 原子化 / 任一失败全回滚 |
| `pkg/saga` | 跨服务 Saga 编排（顺序正向 + 逆序补偿 + 重试） | 抽卡/下单/兑换 等跨服务最终一致 |
| `pkg/loadbalance` | 负载均衡算法（一致性哈希 + 平滑加权轮询 + 轮询） | 会话粘性 / 带状态分片 / 按容量分发 |
| `pkg/idempotency` | 幂等执行（去重 + singleflight 并发合并 + TTL） | 防重复扣款/发奖 / 请求去重 / 缓存击穿保护 |
| `pkg/delayqueue` | 定点单次延迟触发（最小堆 + 可取消/改期） | 开局倒计时 / buff 到期 / 订单超时 / 匹配兜底 |
| `pkg/fsm` | 泛型有限状态机（转移校验 + Enter/Leave 钩子） | 对局/房间/订单状态流转 / 防非法跳转 |
| `pkg/idgen` | 分布式唯一 ID（Snowflake，64 位趋势递增） | 对局 ID / 订单号 / 消息序号 / 数据库主键 |
| `pkg/counter` | 滑动窗口计数 / 时间窗配额 | 每日抽卡上限 / 分钟弹幕限频 / 防刷 |
| `pkg/tally` | 高频累计聚合 + 批量刷写 | 直播点赞/刷礼物 / 埋点计数（削写放大） |
| `pkg/versus` | 限时多方对抗计分（倒计时 + 定胜负 + 事件流） | 直播 PK / 团战 / 答题赛 / 拉票 |
| `pkg/pathfind` | 网格 A* 寻路（障碍 + 权重 + 对角） | 塔防 / SLG / 点击移动 / 怪物追击 |
| `pkg/keyedmutex` | 按 key 的细粒度锁（引用计数自动回收） | 同账户/房间/订单串行 · 不同实体并行 |
| `pkg/momentum` | 连击 + 热度时间衰减（半衰期指数冷却） | 直播连击特效 / 实时热度榜 |
| `pkg/spatial` | 网格空间索引（Nearby / KNN） | 附近的人 / MMO AOI / 大地图分区 |
| `pkg/backoff` | 指数退避 + 抖动重试（Full/Equal/None） | 重试可靠性 · 打散重试风暴 |
| `pkg/eventbus` | 泛型进程内事件总线（按主题 + 回调） | 模块间事件解耦 · 一事件多订阅者 |
| `pkg/geohash` | 经纬度地理编码（编码/邻居/覆盖查询/距离） | LBS 附近的人/店铺（前缀检索） |
| `pkg/ctxkey` | 类型安全 context key（泛型 `Key[T]`） | 统一各包 contextKey 定义 / 防 key 冲突 |

### 业务实体（pkg/domain/）

| 包 | 一句话 | 典型场景 |
|----|--------|----------|
| `pkg/domain/wallet` | 不可变账本 + 余额派生（差值更新）+ txID 幂等 | 虚拟货币 / 积分 / 库存 / 防重复扣款 |
| `pkg/domain/notification` | 持久/瞬时二分 + 离线拉取 | 离线消息 / 系统通知 |
| `pkg/domain/tournament` | 锦标赛（leaderboard + cron 重置） | 赛季榜 / 每日挑战 |
| `pkg/domain/party` | 无权威小队（Leader + 加入审核） | 好友开黑 / 临时小队 |
| `pkg/domain/storage` | 版本化 KV + OCC 乐观锁 | 游戏存档 / 用户配置 |
| `pkg/domain/relationship` | 社交图谱（二部有向图 + 状态编码） | 好友 / 关注 / 拉黑 / 群组 |
| `pkg/domain/chat` | 频道持久消息 + 游标分页 | IM 频道历史 / 翻页 |
| `pkg/domain/inbox` | 收件箱（序列号游标 + 已读状态） | 私信 / 系统收件箱 |
| `pkg/domain/group` | 群组（成员 + 封禁 + 角色） | 群聊 / 公会 / 兴趣小组 |

各包各司其职，无强耦合：可以只用 `ws/session` 做一个 echo 房间，也可以把 `presence` + `router` + `ws/session` 串起来做一个 IM 频道，用 `match` + `matchmaker` 做权威对战大厅，再用 `domain/wallet` + `domain/notification` + `audit` 补齐账户、通知与合规，用 `txn` 把跨域写操作原子化。每个包在 `examples/<name>/` 下都有可运行的 demo。

组合示例：`examples/live-pk` 用 `versus` + `idempotency` + `counter` + `tally` + `keyedmutex` + `eventbus` + `idgen` 搭了一个多房间直播 PK 后端(多局并行 + 送礼幂等去重 + 配额防刷 + 实时比分 SSE + 点赞高频聚合 + 全局 PK 生命周期事件),展示原语如何协作。

详见 [docs/realtime-components.md](docs/realtime-components.md)。

## Documentation

| 文档 | 内容 |
|------|------|
| [docs/configuration.md](docs/configuration.md) | 配置系统：文件 + 远程配置中心 + 热加载 |
| [docs/logger.md](docs/logger.md) | 动态日志级别 |
| [docs/middleware-builtin.md](docs/middleware-builtin.md) | recovery / cors / compress / health |
| [docs/middleware-summary.md](docs/middleware-summary.md) | auth / ratelimit / circuitbreaker / timeout 组合使用 |
| [docs/error-codes.md](docs/error-codes.md) | 结构化错误码：业务 Code / HTTP / gRPC 三层映射、Details、渐进式迁移 |
| [docs/sse.md](docs/sse.md) | Server-Sent Events 推送封装：自动设头 / 写超时 / flush / 断连处理 |
| [docs/websocket.md](docs/websocket.md) | WebSocket 封装（coder/websocket）：升级 / JSON / 子协议 / 心跳保活 |
| [docs/dag.md](docs/dag.md) | DAG 执行器：拓扑分层、层内并行、panic 安全、错误策略 |
| [docs/metadata-propagation.md](docs/metadata-propagation.md) | 服务间 metadata 透传 + OTel trace 传播协议（W3C/B3）|
| [docs/grpc-service-discovery.md](docs/grpc-service-discovery.md) | 服务注册与发现 |
| [docs/grpc-client-discovery.md](docs/grpc-client-discovery.md) | gRPC 客户端发现 |
| [docs/http-client-discovery.md](docs/http-client-discovery.md) | HTTP 客户端服务发现（RoundTripper + 负载均衡 + 重试换节点）|
| [docs/auth-ratelimit.md](docs/auth-ratelimit.md) | 认证与限流详解 |
| [docs/realtime-components.md](docs/realtime-components.md) | 实时服务原语总览（21 类组件 + 组合范式 + demo 端口） |

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the MIT License - see the LICENSE file for details.
