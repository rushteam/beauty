# Beauty 微服务框架中间件系统

本文档介绍 Beauty 微服务框架的中间件系统，包括认证、限流、超时控制、熔断器等核心中间件的使用方法。

## 🚀 中间件特性

### 核心功能
- 🔗 **灵活组合**：支持任意组合多个中间件
- ⚡ **高性能**：中间件链在启动时构建，运行时开销最小
- 🔌 **可扩展**：基于接口的设计，支持自定义扩展
- 📊 **可观测**：提供详细的统计信息和监控能力

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

## ⏱️ 超时控制中间件

### 核心特性
- ⏰ **灵活超时配置**：可配置超时时间和慢请求阈值
- 📊 **统计监控**：详细的超时和性能统计
- 🔔 **回调通知**：超时和慢请求事件回调
- 🔗 **上下文传递**：正确处理 Go context 取消

### 基本用法

```go
// 创建超时控制器
timeoutController := timeout.NewTimeoutController(timeout.Config{
    Name:          "api-timeout",
    Timeout:       5 * time.Second,
    SlowThreshold: 2 * time.Second,
    EnableMetrics: true,
    OnTimeout: func(name string, duration time.Duration) {
        logger.Warn("请求超时", "service", name, "duration", duration)
    },
})

// 在服务器中使用
app := beauty.New(
    beauty.WithService(webserver.New(":8080", handler,
        webserver.WithTimeout(timeoutController),
    )),
)
```

## 🔄 熔断器中间件

### 核心特性
- 🔄 **三种状态**：关闭、开启、半开状态自动切换
- ⚙️ **可配置策略**：失败阈值、恢复时间、半开请求数
- 📊 **状态监控**：详细的熔断统计和状态变化通知
- 🔄 **自动恢复**：智能的故障恢复机制

### 基本用法

```go
// 创建熔断器
circuitBreaker := circuitbreaker.NewCircuitBreaker(circuitbreaker.Config{
    Name:        "api-breaker",
    MaxRequests: 3,
    Interval:    10 * time.Second,
    Timeout:     5 * time.Second,
    ReadyToTrip: func(counts circuitbreaker.Counts) bool {
        return counts.Requests >= 5 &&
            float64(counts.TotalFailures)/float64(counts.Requests) > 0.6
    },
})

// 在服务器中使用
app := beauty.New(
    beauty.WithService(webserver.New(":8080", handler,
        webserver.WithCircuitBreaker(circuitBreaker),
    )),
)
```

## 🔗 中间件组合使用

### 完整的中间件栈

```go
// 创建所有中间件组件
authMiddleware := createAuthMiddleware()
rateLimitMiddleware := createRateLimitMiddleware()
timeoutController := createTimeoutController()
circuitBreaker := createCircuitBreaker()

// 自定义中间件
loggingMiddleware := func(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        next.ServeHTTP(w, r)
        logger.Info("请求完成", 
            "path", r.URL.Path, 
            "duration", time.Since(start))
    })
}

// Web 服务器 - 完整中间件栈
app := beauty.New(
    beauty.WithService(webserver.New(":8080", handler,
        webserver.WithServiceName("api-server"),
        webserver.WithMiddleware(loggingMiddleware),  // 自定义中间件
        webserver.WithAuth(authMiddleware),           // 认证
        webserver.WithRateLimit(rateLimitMiddleware), // 限流
        webserver.WithTimeout(timeoutController),    // 超时控制
        webserver.WithCircuitBreaker(circuitBreaker), // 熔断器
    )),
)
```

### 中间件执行顺序

中间件按照添加顺序执行，形成洋葱模型：

```
请求 -> 日志中间件 -> 认证 -> 限流 -> 超时控制 -> 熔断器 -> 业务处理器
      ↓            ↓      ↓      ↓           ↓
响应 <- 日志中间件 <- 认证 <- 限流 <- 超时控制 <- 熔断器 <- 业务处理器
```

推荐的中间件顺序（从外到内）：
1. **日志中间件** - 记录所有请求
2. **认证中间件** - 验证身份权限
3. **限流中间件** - 控制访问频率
4. **超时控制** - 防止长时间阻塞
5. **熔断器** - 防止级联故障
6. **业务处理器** - 实际业务逻辑

## 📊 监控和统计

### 获取统计信息
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
```