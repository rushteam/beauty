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
- **服务发现**: 支持多种注册中心（etcd/nacos/polaris/k8s）
- **统一配置**: 集中式配置管理
- **中间件**: 完整的中间件支持
- **监控**: 内置链路追踪和指标收集

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

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the MIT License - see the LICENSE file for details.
