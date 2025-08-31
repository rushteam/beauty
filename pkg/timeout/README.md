# 超时控制中间件 (Timeout Control Middleware)

超时控制中间件是一种用于防止请求长时间阻塞的机制。它可以为请求设置最大执行时间，超过时间后自动取消请求，同时提供慢请求检测和详细的统计信息。

## 特性

- **灵活的超时控制**：支持自定义超时时间和慢请求阈值
- **多协议支持**：同时支持 HTTP 和 gRPC
- **详细统计**：提供请求数量、超时率、慢请求率等统计信息
- **慢请求检测**：自动检测并记录慢请求
- **状态回调**：支持超时和慢请求的自定义回调函数
- **管理器模式**：支持管理多个超时控制器实例
- **线程安全**：所有操作都是线程安全的

## 工作原理

超时控制中间件通过以下方式工作：

1. **创建超时上下文**：为每个请求创建带有超时限制的上下文
2. **并发执行**：在 goroutine 中执行实际的请求处理
3. **超时监控**：监控请求执行时间，超时后自动取消
4. **统计收集**：记录请求执行时间、超时次数等统计信息
5. **慢请求检测**：检测执行时间超过阈值的慢请求

## 快速开始

### 基本使用

```go
package main

import (
    "context"
    "time"
    
    "github.com/rushteam/beauty"
    "github.com/rushteam/beauty/pkg/timeout"
)

func main() {
    // 创建超时控制器
    tc := timeout.NewTimeoutController(timeout.DefaultConfig("my-service", 5*time.Second))
    
    // 在应用中使用超时控制
    app := beauty.New(
        beauty.WithWebServerTimeout(":8080", handler, tc),
        beauty.WithGrpcServerTimeout(":9090", grpcHandler, tc),
    )
    
    app.Start(context.Background())
}
```

### 自定义配置

```go
// 使用自定义配置
config := timeout.Config{
    Name:          "api-service",
    Timeout:       10 * time.Second,    // 10秒超时
    SlowThreshold: 3 * time.Second,     // 3秒慢请求阈值
    EnableMetrics: true,
    OnTimeout: func(name string, duration time.Duration) {
        log.Printf("请求超时: %s, 耗时: %s", name, duration)
    },
    OnSlow: func(name string, duration time.Duration) {
        log.Printf("慢请求: %s, 耗时: %s", name, duration)
    },
}

tc := timeout.NewTimeoutController(config)
```

## HTTP 中间件

### 服务端中间件

```go
// 方式1：使用框架提供的便捷方法
app := beauty.New(
    beauty.WithWebServerTimeout(":8080", handler, tc),
)

// 方式2：手动添加中间件
import "net/http"

mux := http.NewServeMux()
mux.HandleFunc("/api", apiHandler)

// 包装处理器
timeoutHandler := timeout.HTTPMiddleware(tc)(mux)

app := beauty.New(
    beauty.WithWebServer(":8080", timeoutHandler),
)
```

### 客户端中间件

```go
import "net/http"

// 创建带超时控制的 HTTP 客户端
client := &http.Client{
    Transport: timeout.HTTPClientMiddleware(tc)(http.DefaultTransport),
}

resp, err := client.Get("http://api.example.com/data")
if timeout.IsHTTPTimeoutError(err) {
    log.Println("请求被超时控制器取消")
}
```

## gRPC 中间件

### 服务端拦截器

```go
// 方式1：使用框架提供的便捷方法
app := beauty.New(
    beauty.WithGrpcServerTimeout(":9090", grpcHandler, tc),
)

// 方式2：手动添加拦截器
import "github.com/rushteam/beauty/pkg/service/grpcserver"

server := grpcserver.New(":9090", grpcHandler,
    grpcserver.WithTimeout(tc),
)
```

### 客户端拦截器

```go
import "google.golang.org/grpc"

conn, err := grpc.Dial("localhost:9090",
    grpc.WithUnaryInterceptor(timeout.UnaryClientInterceptor(tc)),
    grpc.WithStreamInterceptor(timeout.StreamClientInterceptor(tc)),
)
```

## 超时控制器管理器

管理器可以帮助你管理多个超时控制器实例：

```go
// 创建管理器
manager := timeout.NewManager(timeout.ManagerConfig{
    DefaultTimeout:       30 * time.Second,
    DefaultSlowThreshold: 10 * time.Second,
    EnableLogging:        true,
    EnableMetrics:        true,
})

// 获取或创建超时控制器
userServiceTC := manager.GetOrCreate("user-service", 5*time.Second)
orderServiceTC := manager.GetOrCreate("order-service", 10*time.Second)

// 获取所有超时控制器的统计信息
stats := manager.Stats()
for name, stat := range stats {
    fmt.Printf("服务 %s: %s\n", name, stat.String())
}

// 重置所有统计信息
manager.ResetStats()
```

### 使用默认管理器

```go
// 使用全局默认管理器
tc := timeout.GetTimeoutController("my-service", 5*time.Second)

// 获取统计信息
stats := timeout.GetTimeoutControllerStats()

// 重置统计信息
timeout.ResetTimeoutControllerStats("my-service")
```

## 直接使用超时控制器

你也可以直接在代码中使用超时控制器：

```go
// 执行带超时控制的函数
err := tc.Execute(ctx, func(ctx context.Context) error {
    // 你的业务逻辑
    return callExternalService(ctx)
})

if err == timeout.ErrTimeout {
    // 处理超时
    log.Println("请求超时")
} else if err == timeout.ErrTimeoutCanceled {
    // 处理取消
    log.Println("请求被取消")
}

// 执行带返回值的函数
result, err := tc.ExecuteWithResult(ctx, func(ctx context.Context) (interface{}, error) {
    return processData(ctx)
})
```

## 监控和统计

### 获取统计信息

```go
// 获取统计信息
stats := tc.Stats()
fmt.Printf("总请求数: %d\n", stats.TotalRequests)
fmt.Printf("超时请求数: %d\n", stats.TimeoutRequests)
fmt.Printf("慢请求数: %d\n", stats.SlowRequests)
fmt.Printf("平均响应时间: %s\n", stats.AvgDuration)
fmt.Printf("最大响应时间: %s\n", stats.MaxDuration)
fmt.Printf("最小响应时间: %s\n", stats.MinDuration)

// 获取比率
fmt.Printf("超时率: %.2f%%\n", tc.TimeoutRate()*100)
fmt.Printf("慢请求率: %.2f%%\n", tc.SlowRate()*100)
```

### HTTP 监控端点

```go
// 添加监控端点
mux.HandleFunc("/timeout/status", func(w http.ResponseWriter, r *http.Request) {
    stats := timeout.GetTimeoutControllerStats()
    json.NewEncoder(w).Encode(stats)
})

mux.HandleFunc("/timeout/reset", func(w http.ResponseWriter, r *http.Request) {
    if r.Method == http.MethodPost {
        timeout.ResetAllTimeoutControllerStats()
        w.Write([]byte("统计信息已重置"))
    }
})
```

## 配置参数说明

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| Name | string | "timeout-controller" | 超时控制器名称 |
| Timeout | time.Duration | 30秒 | 超时时间 |
| SlowThreshold | time.Duration | Timeout/2 | 慢请求阈值 |
| OnTimeout | func(string, time.Duration) | 默认日志记录 | 超时时的回调函数 |
| OnSlow | func(string, time.Duration) | 默认日志记录 | 慢请求时的回调函数 |
| EnableMetrics | bool | true | 是否启用指标统计 |

## 最佳实践

1. **合理设置超时时间**：根据业务需求设置合适的超时时间，避免过短或过长
2. **监控慢请求**：使用慢请求阈值来识别性能问题
3. **分服务配置**：不同的服务使用不同的超时配置
4. **优雅处理超时**：在超时发生时提供合适的错误处理和用户反馈
5. **定期监控统计**：定期查看超时率和慢请求率，及时发现性能问题

## 错误处理

```go
err := tc.Execute(ctx, func(ctx context.Context) error {
    return callExternalService(ctx)
})

if err != nil {
    switch err {
    case timeout.ErrTimeout:
        // 请求超时
        return handleTimeout()
    case timeout.ErrTimeoutCanceled:
        // 请求被取消
        return handleCancellation()
    default:
        // 其他业务错误
        return handleBusinessError(err)
    }
}
```

## 与其他中间件组合使用

超时控制中间件可以与其他中间件（如熔断器）组合使用：

```go
// 创建超时控制器和熔断器
tc := timeout.NewTimeoutController(timeout.DefaultConfig("service", 5*time.Second))
cb := circuitbreaker.NewCircuitBreaker(circuitbreaker.DefaultConfig("service"))

// 组合使用（注意顺序：超时控制器应该在熔断器内层）
app := beauty.New(
    beauty.WithService(webserver.New(":8080", mux,
        webserver.WithCircuitBreaker(cb),    // 外层：熔断器
        webserver.WithTimeout(tc),           // 内层：超时控制
    )),
)
```

## 示例项目

查看 `example/timeout/main.go` 获取完整的使用示例。

运行示例：
```bash
cd example/timeout
go run main.go
```

访问以下端点：
- http://localhost:8080/timeout/status - 查看超时统计
- POST http://localhost:8080/timeout/reset - 重置统计信息
- http://localhost:8080/fast - 快速响应测试
- http://localhost:8080/slow - 慢响应测试
- http://localhost:8080/timeout - 超时响应测试
- http://localhost:8080/random - 随机响应时间测试
- http://localhost:8080/test - 手动超时控制测试
