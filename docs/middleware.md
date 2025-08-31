# 中间件系统设计 (Middleware System)

本文档描述了 Beauty 微服务框架的中间件系统设计，以及如何灵活地组合使用多个中间件。

## 设计原则

### 1. 组合优于继承
新的中间件系统采用组合模式，允许用户灵活地组合多个中间件，而不是为每种组合创建专门的函数。

### 2. 中间件链
中间件按照添加的顺序形成一个处理链，请求会依次通过每个中间件。

### 3. 统一接口
HTTP 和 gRPC 都采用统一的中间件接口模式，保持 API 的一致性。

## HTTP 中间件系统

### 基本用法

```go
// 创建 Web 服务器，同时使用多个中间件
app := beauty.New(
    beauty.WithService(webserver.New(":8080", handler,
        webserver.WithServiceName("web-server"),
        webserver.WithMiddleware(loggingMiddleware),    // 自定义中间件
        webserver.WithTimeout(timeoutController),      // 超时控制
        webserver.WithCircuitBreaker(circuitBreaker),  // 熔断器
    )),
)
```

### 中间件执行顺序

中间件按照**添加顺序**执行，形成洋葱模型：

```
请求 -> 日志中间件 -> 超时控制 -> 熔断器 -> 业务处理器 -> 熔断器 -> 超时控制 -> 日志中间件 -> 响应
```

### 自定义中间件

```go
// 创建自定义中间件
func loggingMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        next.ServeHTTP(w, r)
        duration := time.Since(start)
        log.Printf("Request: %s %s, Duration: %s", r.Method, r.URL.Path, duration)
    })
}

// 使用自定义中间件
webserver.New(":8080", handler,
    webserver.WithMiddleware(loggingMiddleware),
)
```

### 多个中间件组合

```go
webserver.New(":8080", handler,
    webserver.WithServiceName("api-server"),
    // 中间件执行顺序：认证 -> 限流 -> 超时 -> 熔断 -> 业务逻辑
    webserver.WithMiddleware(authMiddleware),      // 认证中间件
    webserver.WithMiddleware(rateLimitMiddleware), // 限流中间件
    webserver.WithTimeout(tc),                     // 超时控制
    webserver.WithCircuitBreaker(cb),             // 熔断器
)
```

## gRPC 中间件系统

### 基本用法

```go
// 创建 gRPC 服务器，同时使用多个拦截器
app := beauty.New(
    beauty.WithService(grpcserver.New(":9090", grpcHandler,
        grpcserver.WithServiceName("grpc-server"),
        grpcserver.WithTimeout(timeoutController),      // 超时控制拦截器
        grpcserver.WithCircuitBreaker(circuitBreaker),  // 熔断器拦截器
    )),
)
```

### 自定义拦截器

```go
// 创建自定义一元拦截器
func loggingUnaryInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
    start := time.Now()
    resp, err := handler(ctx, req)
    duration := time.Since(start)
    log.Printf("gRPC Call: %s, Duration: %s", info.FullMethod, duration)
    return resp, err
}

// 使用自定义拦截器
grpcserver.New(":9090", handler,
    grpcserver.WithGrpcServerUnaryInterceptor(loggingUnaryInterceptor),
)
```

### 拦截器链

gRPC 拦截器也形成链式调用：

```go
grpcserver.New(":9090", handler,
    grpcserver.WithServiceName("grpc-server"),
    // 拦截器执行顺序：认证 -> 超时 -> 熔断 -> 业务逻辑
    grpcserver.WithGrpcServerUnaryInterceptor(authInterceptor),
    grpcserver.WithTimeout(tc),
    grpcserver.WithCircuitBreaker(cb),
)
```

## 中间件最佳实践

### 1. 中间件顺序

建议的中间件执行顺序（从外到内）：

1. **日志/监控中间件** - 记录所有请求
2. **认证/授权中间件** - 验证请求权限
3. **限流中间件** - 控制请求频率
4. **超时控制中间件** - 防止请求长时间阻塞
5. **熔断器中间件** - 防止级联故障
6. **业务处理器** - 实际的业务逻辑

### 2. 错误处理

每个中间件都应该妥善处理错误：

```go
func errorHandlingMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        defer func() {
            if err := recover(); err != nil {
                log.Printf("Panic recovered: %v", err)
                http.Error(w, "Internal Server Error", http.StatusInternalServerError)
            }
        }()
        next.ServeHTTP(w, r)
    })
}
```

### 3. 上下文传递

利用 context 在中间件间传递信息：

```go
func contextMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // 在上下文中添加信息
        ctx := context.WithValue(r.Context(), "requestID", generateRequestID())
        r = r.WithContext(ctx)
        next.ServeHTTP(w, r)
    })
}
```

## 内置中间件

### 1. 超时控制中间件

```go
// 创建超时控制器
tc := timeout.NewTimeoutController(timeout.Config{
    Name:          "api-timeout",
    Timeout:       5 * time.Second,
    SlowThreshold: 2 * time.Second,
})

// 使用超时中间件
webserver.WithTimeout(tc)
grpcserver.WithTimeout(tc)
```

### 2. 熔断器中间件

```go
// 创建熔断器
cb := circuitbreaker.NewCircuitBreaker(circuitbreaker.Config{
    Name:        "api-breaker",
    MaxRequests: 5,
    Interval:    time.Minute,
    Timeout:     30 * time.Second,
})

// 使用熔断器中间件
webserver.WithCircuitBreaker(cb)
grpcserver.WithCircuitBreaker(cb)
```

## 完整示例

```go
package main

import (
    "context"
    "net/http"
    "time"
    
    "github.com/rushteam/beauty"
    "github.com/rushteam/beauty/pkg/circuitbreaker"
    "github.com/rushteam/beauty/pkg/service/grpcserver"
    "github.com/rushteam/beauty/pkg/service/webserver"
    "github.com/rushteam/beauty/pkg/timeout"
    "google.golang.org/grpc"
)

func main() {
    // 创建中间件组件
    tc := timeout.NewTimeoutController(timeout.DefaultConfig("service", 5*time.Second))
    cb := circuitbreaker.NewCircuitBreaker(circuitbreaker.DefaultConfig("service"))
    
    // 自定义中间件
    loggingMiddleware := func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            start := time.Now()
            next.ServeHTTP(w, r)
            log.Printf("Request: %s %s, Duration: %s", 
                r.Method, r.URL.Path, time.Since(start))
        })
    }
    
    // HTTP 路由
    mux := http.NewServeMux()
    mux.HandleFunc("/api", apiHandler)
    
    app := beauty.New(
        // Web 服务器 - 多中间件组合
        beauty.WithService(webserver.New(":8080", mux,
            webserver.WithServiceName("web-server"),
            webserver.WithMiddleware(loggingMiddleware),  // 日志
            webserver.WithTimeout(tc),                    // 超时控制
            webserver.WithCircuitBreaker(cb),            // 熔断器
        )),
        
        // gRPC 服务器 - 多拦截器组合
        beauty.WithService(grpcserver.New(":9090", func(s *grpc.Server) {
            // 注册 gRPC 服务
        },
            grpcserver.WithServiceName("grpc-server"),
            grpcserver.WithTimeout(tc),           // 超时控制
            grpcserver.WithCircuitBreaker(cb),    // 熔断器
        )),
    )
    
    app.Start(context.Background())
}

func apiHandler(w http.ResponseWriter, r *http.Request) {
    w.Write([]byte("Hello, World!"))
}
```

## 与旧版本的对比

### 旧版本（不推荐）
```go
// 旧版本：每种组合都需要专门的函数
beauty.WithWebServerTimeout(":8080", handler, tc)
beauty.WithWebServerCircuitBreaker(":8080", handler, cb)
// 无法同时使用超时和熔断器
```

### 新版本（推荐）
```go
// 新版本：灵活组合任意中间件
beauty.WithService(webserver.New(":8080", handler,
    webserver.WithTimeout(tc),
    webserver.WithCircuitBreaker(cb),
    webserver.WithMiddleware(customMiddleware),
))
```

## 总结

新的中间件系统提供了以下优势：

1. **灵活性**：可以任意组合多个中间件
2. **可扩展性**：容易添加新的中间件
3. **一致性**：HTTP 和 gRPC 使用相同的设计模式
4. **可维护性**：避免了为每种组合创建专门函数的复杂性
5. **性能**：中间件链在服务器创建时构建，运行时开销最小

这种设计使得框架更加灵活和易用，同时保持了良好的性能和可维护性。
