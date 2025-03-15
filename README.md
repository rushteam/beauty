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

## Project Structure

A typical Beauty project has the following structure:

```
my-service/
├── config/
│   └── dev/
│       └── app.yaml
├── go.mod
├── main.go
└── internal/
    ├── config/
    │   └── config.go
    ├── endpoint/
    │   └── router/
    │       ├── http.go
    │       ├── middleware.go
    │       └── router.go
    └── infra/
        ├── conf/
        └── logger/
```

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
    "github.com/rushteam/beauty/pkg/discover/etcdv3"
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

## Tracing and Metrics

Beauty provides built-in support for OpenTelemetry tracing and metrics:

```go
import (
    "github.com/rushteam/beauty"
    "github.com/rushteam/beauty/pkg/tracing"
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
        beauty.WithMetric(tracing.WithMetricReader(metricExporter)), // Enable metrics
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
    "github.com/rushteam/beauty/pkg/discover/etcdv3"
    "github.com/rushteam/beauty/pkg/tracing"
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
