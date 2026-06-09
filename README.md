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
- **服务发现**: 支持多种注册中心（etcd / nacos / consul / polaris / k8s）
- **配置中心**: 统一 `conf.New(url)` 接入本地文件与远程配置，支持热加载
- **中间件**: recovery、cors、compress、health、auth、限流、熔断、超时
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

## Documentation

| 文档 | 内容 |
|------|------|
| [docs/configuration.md](docs/configuration.md) | 配置系统：文件 + 远程配置中心 + 热加载 |
| [docs/logger.md](docs/logger.md) | 动态日志级别 |
| [docs/middleware-builtin.md](docs/middleware-builtin.md) | recovery / cors / compress / health |
| [docs/middleware-summary.md](docs/middleware-summary.md) | auth / ratelimit / circuitbreaker / timeout 组合使用 |
| [docs/error-codes.md](docs/error-codes.md) | 结构化错误码：业务 Code / HTTP / gRPC 三层映射、Details、渐进式迁移 |
| [docs/metadata-propagation.md](docs/metadata-propagation.md) | 服务间 metadata 透传 + OTel trace 传播协议（W3C/B3）|
| [docs/grpc-service-discovery.md](docs/grpc-service-discovery.md) | 服务注册与发现 |
| [docs/grpc-client-discovery.md](docs/grpc-client-discovery.md) | gRPC 客户端发现 |
| [docs/auth-ratelimit.md](docs/auth-ratelimit.md) | 认证与限流详解 |

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the MIT License - see the LICENSE file for details.
