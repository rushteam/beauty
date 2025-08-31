# Beauty 微服务框架中间件系统

本文档介绍 Beauty 微服务框架的中间件系统，包括认证、限流、超时控制、熔断器等核心中间件的使用方法。

## 🚀 系统特性

### 核心优势
- 🔗 **灵活组合**：支持任意组合多个中间件
- ⚡ **高性能**：中间件链在启动时构建，运行时开销最小
- 🔌 **可扩展**：基于接口的设计，支持自定义扩展
- 📊 **可观测**：提供详细的统计信息和监控能力
- 🎯 **统一设计**：HTTP 和 gRPC 使用一致的中间件模式

### 内置中间件
- 🔐 **认证中间件**：多种认证方式，灵活的授权机制
- 🚦 **限流中间件**：多种限流策略，动态参数调整
- ⏱️ **超时控制**：请求超时保护，慢请求监控
- 🔄 **熔断器**：故障隔离，自动恢复机制

## 🔐 认证中间件

### 核心特性
- 🔑 **多种令牌提取器**：Header、Query、Cookie、gRPC Metadata、多源提取器
- 🔐 **可扩展认证器**：静态令牌、JWT、回调认证器、链式认证器  
- 👮 **灵活授权机制**：基于角色、基于路径、回调授权器
- 📊 **完整统计信息**：认证成功率、失败统计等

### 基本用法

```go
// 创建认证中间件
authMiddleware := auth.NewAuthMiddleware(auth.Config{
    Name: "api-auth",
    TokenExtractor: auth.NewHeaderTokenExtractor("Authorization", "Bearer "),
    Authenticator:  yourAuthenticator,
    SkipPaths:     []string{"/health", "/public"},
    EnableMetrics: true,
})

// 在服务器中使用
app := beauty.New(
    beauty.WithService(webserver.New(":8080", handler,
        webserver.WithAuth(authMiddleware),
    )),
    beauty.WithService(grpcserver.New(":9090", grpcHandler,
        grpcserver.WithAuth(authMiddleware),
    )),
)
```

## 🚦 限流中间件

### 核心特性
- 🎯 **多种限流策略**：IP 限流、用户限流、路径限流、自定义键
- ⚡ **高性能实现**：基于令牌桶算法，线程安全
- 🔄 **灵活模式**：直接拒绝模式和等待模式
- 📈 **动态调整**：运行时更新限流参数

### 基本用法

```go
// 创建限流中间件
rateLimitMiddleware := ratelimit.NewRateLimitMiddleware(ratelimit.Config{
    Name: "api-ratelimit", 
    Rate: 100.0, // 每秒100个请求
    Burst: 200,  // 突发容量200
    KeyExtractor: ratelimit.NewIPKeyExtractor(),
    EnableMetrics: true,
})

// 在服务器中使用
app := beauty.New(
    beauty.WithService(webserver.New(":8080", handler,
        webserver.WithRateLimit(rateLimitMiddleware),     // 直接拒绝
        // 或
        webserver.WithRateLimitWait(rateLimitMiddleware), // 等待通过
    )),
)
```

## 🔗 中间件组合使用

### 完整的中间件栈

```go
// 创建 Web 服务器，同时使用多个中间件
app := beauty.New(
    beauty.WithService(webserver.New(":8080", handler,
        webserver.WithServiceName("web-server"),
        webserver.WithMiddleware(loggingMiddleware),  // 自定义中间件
        webserver.WithAuth(authMiddleware),           // 认证中间件
        webserver.WithRateLimit(rateLimitMiddleware), // 限流中间件
        webserver.WithTimeout(timeoutController),    // 超时控制
        webserver.WithCircuitBreaker(circuitBreaker), // 熔断器
    )),
)
```

### 中间件执行顺序

中间件按照**添加顺序**执行，形成洋葱模型：

```
请求 -> 日志中间件 -> 认证 -> 限流 -> 超时控制 -> 熔断器 -> 业务处理器 -> 熔断器 -> 超时控制 -> 限流 -> 认证 -> 日志中间件 -> 响应
```

## 🛠️ 自定义扩展

### 扩展能力特性
- 🔌 **接口化设计**：业务方可以实现自定义认证、授权、限流逻辑
- 🛠️ **回调机制**：支持自定义认证器、授权器、键提取器
- 📦 **组合模式**：支持多个组件的灵活组合

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

### 自定义认证器

```go
type MyAuthenticator struct {
    authService AuthService
}

func (a *MyAuthenticator) Authenticate(ctx context.Context, token string) (auth.User, error) {
    // 实现你的认证逻辑
    userInfo, err := a.authService.ValidateToken(token)
    if err != nil {
        return nil, auth.ErrInvalidToken
    }
    return auth.NewUser(userInfo.ID, userInfo.Name, userInfo.Roles), nil
}

// 使用自定义认证器
authMiddleware := auth.NewAuthMiddleware(auth.Config{
    Authenticator: &MyAuthenticator{authService: yourAuthService},
})
```

### 自定义限流键提取器

```go
type MyKeyExtractor struct{}

func (e *MyKeyExtractor) Extract(ctx context.Context, metadata map[string]interface{}) (string, error) {
    // 实现你的键提取逻辑
    if tenantID, ok := metadata["tenant_id"].(string); ok {
        return "tenant:" + tenantID, nil
    }
    return "default", nil
}

// 使用自定义键提取器
rateLimitMiddleware := ratelimit.NewRateLimitMiddleware(ratelimit.Config{
    KeyExtractor: &MyKeyExtractor{},
})
```

## 📊 监控和统计

### 统计信息获取

```go
// 认证统计
authStats := authMiddleware.Stats()
fmt.Printf("认证成功率: %.2f%%\n", authMiddleware.SuccessRate()*100)

// 限流统计  
rlStats := rateLimitMiddleware.Stats()
fmt.Printf("限流率: %.2f%%\n", 
    float64(rlStats.LimitedRequests)/float64(rlStats.TotalRequests)*100)

// 超时统计
tcStats := timeoutController.Stats()
fmt.Printf("超时率: %.2f%%\n", timeoutController.TimeoutRate()*100)

// 熔断器统计
cbStats := circuitBreaker.Counts()
fmt.Printf("熔断器状态: %s\n", circuitBreaker.State().String())
```

### 监控端点

```go
// 统一状态监控端点
mux.HandleFunc("/middleware/status", func(w http.ResponseWriter, r *http.Request) {
    response := map[string]interface{}{
        "auth":           authMiddleware.Stats(),
        "rate_limit":     rateLimitMiddleware.Stats(), 
        "timeout":        timeoutController.Stats(),
        "circuit_breaker": circuitBreaker.Counts(),
    }
    json.NewEncoder(w).Encode(response)
})

// 动态配置管理
mux.HandleFunc("/admin/ratelimit/update", func(w http.ResponseWriter, r *http.Request) {
    newRate := parseFloat(r.FormValue("rate"))
    newBurst := parseInt(r.FormValue("burst"))
    
    rateLimitMiddleware.UpdateRate(newRate, newBurst)
    w.Write([]byte("Rate limit updated"))
})
```

## 💡 最佳实践

### 1. 中间件顺序

推荐的中间件执行顺序（从外到内）：

1. **日志中间件** - 记录所有请求
2. **认证中间件** - 验证身份权限
3. **限流中间件** - 控制访问频率
4. **超时控制** - 防止长时间阻塞
5. **熔断器** - 防止级联故障
6. **业务处理器** - 实际业务逻辑

### 2. 错误处理

```go
// 检查认证错误
if auth.IsAuthError(err) {
    // 处理认证错误
}

// 检查限流错误
if ratelimit.IsRateLimitError(err) {
    // 处理限流错误
}

// 获取用户信息
if user, ok := auth.GetUserFromContext(ctx); ok {
    // 使用用户信息
}
```

### 3. 配置管理

```go
// 使用配置文件
authConfig := auth.Config{
    Name: viper.GetString("auth.name"),
    SkipPaths: viper.GetStringSlice("auth.skip_paths"),
    EnableMetrics: viper.GetBool("auth.enable_metrics"),
}

rateLimitConfig := ratelimit.Config{
    Name: viper.GetString("ratelimit.name"),
    Rate: viper.GetFloat64("ratelimit.rate"),
    Burst: viper.GetInt("ratelimit.burst"),
}
```

## 完整示例

```go
package main

import (
    "context"
    "log"
    "net/http"
    "time"
    
    "github.com/rushteam/beauty"
    "github.com/rushteam/beauty/pkg/auth"
    "github.com/rushteam/beauty/pkg/circuitbreaker"
    "github.com/rushteam/beauty/pkg/ratelimit"
    "github.com/rushteam/beauty/pkg/service/grpcserver"
    "github.com/rushteam/beauty/pkg/service/webserver"
    "github.com/rushteam/beauty/pkg/timeout"
    "google.golang.org/grpc"
)

func main() {
    // 创建认证中间件
    authMiddleware := auth.NewAuthMiddleware(auth.Config{
        Name: "api-auth",
        TokenExtractor: auth.NewHeaderTokenExtractor("Authorization", "Bearer "),
        Authenticator: yourAuthenticator,
        SkipPaths: []string{"/health", "/public"},
    })

    // 创建限流中间件
    rateLimitMiddleware := ratelimit.NewRateLimitMiddleware(ratelimit.Config{
        Name: "api-ratelimit",
        Rate: 100.0,
        Burst: 200,
        KeyExtractor: ratelimit.NewIPKeyExtractor(),
    })

    // 创建超时控制器
    timeoutController := timeout.NewTimeoutController(timeout.Config{
        Name: "api-timeout",
        Timeout: 5 * time.Second,
    })

    // 创建熔断器
    circuitBreaker := circuitbreaker.NewCircuitBreaker(circuitbreaker.Config{
        Name: "api-breaker",
        MaxRequests: 5,
        Interval: time.Minute,
    })
    
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
    mux.HandleFunc("/api/users", usersHandler)
    mux.HandleFunc("/public", publicHandler)
    
    app := beauty.New(
        // Web 服务器 - 完整中间件栈
        beauty.WithService(webserver.New(":8080", mux,
            webserver.WithServiceName("api-server"),
            webserver.WithMiddleware(loggingMiddleware),  // 日志
            webserver.WithAuth(authMiddleware),           // 认证
            webserver.WithRateLimit(rateLimitMiddleware), // 限流
            webserver.WithTimeout(timeoutController),    // 超时控制
            webserver.WithCircuitBreaker(circuitBreaker), // 熔断器
        )),
        
        // gRPC 服务器 - 完整拦截器栈
        beauty.WithService(grpcserver.New(":9090", func(s *grpc.Server) {
            // 注册 gRPC 服务
        },
            grpcserver.WithServiceName("grpc-server"),
            grpcserver.WithAuth(authMiddleware),           // 认证
            grpcserver.WithRateLimit(rateLimitMiddleware), // 限流
            grpcserver.WithTimeout(timeoutController),    // 超时控制
            grpcserver.WithCircuitBreaker(circuitBreaker), // 熔断器
        )),
    )
    
    app.Start(context.Background())
}

func usersHandler(w http.ResponseWriter, r *http.Request) {
    w.Write([]byte("Users API"))
}

func publicHandler(w http.ResponseWriter, r *http.Request) {
    w.Write([]byte("Public endpoint"))
}
```

这个中间件系统提供了强大的扩展能力，业务方可以通过实现相应的接口来自定义认证、授权和限流逻辑，同时保持了框架的一致性和易用性。
