# 熔断器中间件 (Circuit Breaker Middleware)

熔断器是一种用于防止级联故障的设计模式。当服务出现故障时，熔断器会快速失败，避免对故障服务的持续调用，从而保护整个系统的稳定性。

## 特性

- **三种状态**：关闭（Closed）、开启（Open）、半开（Half-Open）
- **灵活配置**：支持自定义失败阈值、超时时间、统计窗口等
- **多协议支持**：同时支持 HTTP 和 gRPC
- **状态监控**：提供详细的统计信息和状态变化回调
- **管理器模式**：支持管理多个熔断器实例
- **线程安全**：所有操作都是线程安全的

## 工作原理

### 状态转换

```
关闭状态 (Closed) ──失败率超过阈值──> 开启状态 (Open)
     ↑                                    ↓
     └──连续成功──── 半开状态 (Half-Open) ←──超时──┘
                         ↓
                      失败一次
                         ↓
                   开启状态 (Open)
```

1. **关闭状态 (Closed)**：正常处理所有请求，统计成功和失败次数
2. **开启状态 (Open)**：拒绝所有请求，直接返回错误
3. **半开状态 (Half-Open)**：允许少量请求通过，测试服务是否恢复

## 快速开始

### 基本使用

```go
package main

import (
    "context"
    "time"
    
    "github.com/rushteam/beauty"
    "github.com/rushteam/beauty/pkg/circuitbreaker"
)

func main() {
    // 创建熔断器配置
    config := circuitbreaker.DefaultConfig("my-service")
    
    // 创建熔断器
    cb := circuitbreaker.NewCircuitBreaker(config)
    
    // 在应用中使用熔断器
    app := beauty.New(
        beauty.WithWebServerCircuitBreaker(":8080", handler, cb),
        beauty.WithGrpcServerCircuitBreaker(":9090", grpcHandler, cb),
    )
    
    app.Start(context.Background())
}
```

### 自定义配置

```go
// 使用配置构建器
config := circuitbreaker.NewCustomConfig("api-service").
    WithMaxRequests(5).                    // 半开状态下最大请求数
    WithInterval(30 * time.Second).        // 统计窗口
    WithTimeout(60 * time.Second).         // 熔断超时时间
    WithReadyToTrip(func(counts circuitbreaker.Counts) bool {
        // 自定义熔断条件：请求数超过10且失败率超过50%
        return counts.Requests >= 10 && 
               float64(counts.TotalFailures)/float64(counts.Requests) > 0.5
    }).
    WithOnStateChange(func(name string, from, to circuitbreaker.State) {
        log.Printf("熔断器 %s 状态从 %s 变为 %s", name, from, to)
    }).
    Build()

cb := circuitbreaker.NewCircuitBreaker(config)
```

### 预定义配置

```go
// 高敏感度配置（更容易触发熔断）
config := circuitbreaker.HighSensitivityConfig("sensitive-service")

// 低敏感度配置（不容易触发熔断）
config := circuitbreaker.LowSensitivityConfig("stable-service")

// 基于连续失败次数的配置
config := circuitbreaker.ConsecutiveFailuresConfig("critical-service", 5)
```

## HTTP 中间件

### 服务端中间件

```go
// 方式1：使用框架提供的便捷方法
app := beauty.New(
    beauty.WithWebServerCircuitBreaker(":8080", handler, cb),
)

// 方式2：手动添加中间件
import "github.com/go-chi/chi/v5"

r := chi.NewRouter()
r.Use(circuitbreaker.HTTPMiddleware(cb))
r.Get("/api", apiHandler)

app := beauty.New(
    beauty.WithWebServer(":8080", r),
)
```

### 客户端中间件

```go
import "net/http"

// 创建带熔断器的 HTTP 客户端
client := &http.Client{
    Transport: circuitbreaker.HTTPClientMiddleware(cb)(http.DefaultTransport),
}

resp, err := client.Get("http://api.example.com/data")
if circuitbreaker.IsCircuitBreakerError(err) {
    log.Println("请求被熔断器拒绝")
}
```

## gRPC 中间件

### 服务端拦截器

```go
// 方式1：使用框架提供的便捷方法
app := beauty.New(
    beauty.WithGrpcServerCircuitBreaker(":9090", grpcHandler, cb),
)

// 方式2：手动添加拦截器
import "github.com/rushteam/beauty/pkg/service/grpcserver"

server := grpcserver.New(":9090", grpcHandler,
    grpcserver.WithCircuitBreaker(cb),
)
```

### 客户端拦截器

```go
import "google.golang.org/grpc"

conn, err := grpc.Dial("localhost:9090",
    grpc.WithUnaryInterceptor(circuitbreaker.UnaryClientInterceptor(cb)),
    grpc.WithStreamInterceptor(circuitbreaker.StreamClientInterceptor(cb)),
)
```

## 熔断器管理器

管理器可以帮助你管理多个熔断器实例：

```go
// 创建管理器
manager := circuitbreaker.NewManager(circuitbreaker.ManagerConfig{
    DefaultConfig:  circuitbreaker.DefaultConfig("default"),
    EnableLogging:  true,
    LogStateChange: true,
})

// 获取或创建熔断器
userServiceCB := manager.GetOrCreate("user-service")
orderServiceCB := manager.GetOrCreate("order-service", 
    circuitbreaker.HighSensitivityConfig("order-service"))

// 获取所有熔断器的统计信息
stats := manager.Stats()
for name, stat := range stats {
    fmt.Printf("服务 %s: %s\n", name, stat.String())
}

// 重置所有熔断器
manager.Reset()
```

### 使用默认管理器

```go
// 使用全局默认管理器
cb := circuitbreaker.GetCircuitBreaker("my-service")

// 获取统计信息
stats := circuitbreaker.GetCircuitBreakerStats()

// 重置熔断器
circuitbreaker.ResetCircuitBreaker("my-service")
```

## 监控和调试

### 状态查询

```go
// 获取当前状态
state := cb.State() // StateClosed, StateOpen, StateHalfOpen

// 获取统计信息
counts := cb.Counts()
fmt.Printf("请求数: %d, 成功: %d, 失败: %d\n", 
    counts.Requests, counts.TotalSuccesses, counts.TotalFailures)
```

### HTTP 监控端点

```go
// 添加监控端点
r.Get("/circuit-breaker/status", func(w http.ResponseWriter, r *http.Request) {
    stats := circuitbreaker.GetCircuitBreakerStats()
    json.NewEncoder(w).Encode(stats)
})

r.Post("/circuit-breaker/reset/{name}", func(w http.ResponseWriter, r *http.Request) {
    name := chi.URLParam(r, "name")
    if circuitbreaker.ResetCircuitBreaker(name) {
        w.Write([]byte("重置成功"))
    } else {
        http.Error(w, "熔断器不存在", http.StatusNotFound)
    }
})
```

## 配置参数说明

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| Name | string | "CircuitBreaker" | 熔断器名称 |
| MaxRequests | uint32 | 1 | 半开状态下允许的最大请求数 |
| Interval | time.Duration | 1分钟 | 统计窗口时间间隔 |
| Timeout | time.Duration | 1分钟 | 熔断器开启后的超时时间 |
| ReadyToTrip | func(Counts) bool | 默认规则 | 判断是否应该熔断的函数 |
| OnStateChange | func(string, State, State) | nil | 状态变化时的回调函数 |

## 最佳实践

1. **合理设置阈值**：根据服务的实际情况设置失败率阈值和请求数阈值
2. **监控状态变化**：使用 OnStateChange 回调记录状态变化，便于问题排查
3. **分服务配置**：不同的服务使用不同的熔断器配置
4. **优雅降级**：在熔断器开启时提供备用方案或缓存数据
5. **定期重置**：在服务恢复后及时重置熔断器状态

## 错误处理

```go
err := cb.Call(func() error {
    return callExternalService()
})

if err != nil {
    switch err {
    case circuitbreaker.ErrCircuitBreakerOpen:
        // 熔断器开启，使用备用方案
        return fallbackResponse()
    case circuitbreaker.ErrTooManyRequests:
        // 半开状态下请求过多
        return rateLimitResponse()
    default:
        // 其他业务错误
        return handleBusinessError(err)
    }
}
```

## 示例项目

查看 `example/circuitbreaker/main.go` 获取完整的使用示例。

运行示例：
```bash
cd example/circuitbreaker
go run main.go
```

访问以下端点：
- http://localhost:8080/test - 测试熔断器
- http://localhost:8080/circuit-breaker/status - 查看状态
- POST http://localhost:8080/circuit-breaker/reset - 重置熔断器
