# Beauty 微服务框架中间件系统总览

## 🚀 新的中间件架构

我们重新设计了中间件系统，解决了原有接口不够灵活的问题，现在支持任意组合多个中间件。

### ❌ 旧设计的问题
```go
// 旧设计：每种组合都需要专门的函数
beauty.WithWebServerTimeout(":8080", handler, tc)
beauty.WithWebServerCircuitBreaker(":8080", handler, cb)
// 无法同时使用多个中间件！
```

### ✅ 新设计的优势
```go
// 新设计：灵活组合任意中间件
beauty.WithService(webserver.New(":8080", handler,
    webserver.WithAuth(authMiddleware),           // 认证
    webserver.WithRateLimit(rateLimitMiddleware), // 限流
    webserver.WithTimeout(timeoutController),    // 超时控制
    webserver.WithCircuitBreaker(circuitBreaker), // 熔断器
    webserver.WithMiddleware(customMiddleware),   // 自定义中间件
))
```

## 🛡️ 内置中间件功能

### 1. 认证中间件 (Authentication)

**核心特性：**
- 🔑 多种令牌提取方式（Header、Query、Cookie、gRPC Metadata）
- 🔐 可扩展的认证器接口（静态令牌、JWT、自定义回调）
- 👮 灵活的授权机制（角色、路径、自定义规则）
- 📊 详细的认证统计信息
- ⚡ 高性能和线程安全

**使用示例：**
```go
// 创建认证中间件
authMiddleware := auth.NewAuthMiddleware(auth.Config{
    Name: "api-auth",
    TokenExtractor: auth.NewMultiTokenExtractor(
        auth.NewHeaderTokenExtractor("Authorization", "Bearer "),
        auth.NewQueryTokenExtractor("token"),
    ),
    Authenticator: yourCustomAuthenticator,
    SkipPaths:    []string{"/health", "/public"},
})

// 在服务器中使用
webserver.WithAuth(authMiddleware)
grpcserver.WithAuth(authMiddleware)
```

### 2. 限流中间件 (Rate Limiting)

**核心特性：**
- 🎯 多种限流策略（IP、用户、路径、自定义键）
- ⚡ 基于令牌桶算法的高性能实现
- 🔄 支持等待模式和直接拒绝模式
- 📈 动态调整限流参数
- 📊 详细的限流统计信息

**使用示例：**
```go
// 创建限流中间件
rateLimitMiddleware := ratelimit.NewRateLimitMiddleware(ratelimit.Config{
    Name: "api-ratelimit",
    Rate: 100.0, // 每秒100个请求
    Burst: 200,  // 突发容量200
    KeyExtractor: ratelimit.NewChainKeyExtractor(
        ratelimit.NewUserKeyExtractor("user_id"), // 优先按用户限流
        ratelimit.NewIPKeyExtractor(),             // 其次按IP限流
    ),
})

// 在服务器中使用
webserver.WithRateLimit(rateLimitMiddleware)      // 直接拒绝
webserver.WithRateLimitWait(rateLimitMiddleware)  // 等待通过
grpcserver.WithRateLimit(rateLimitMiddleware)
```

### 3. 超时控制中间件 (Timeout Control)

**核心特性：**
- ⏱️ 灵活的超时时间配置
- 🐌 慢请求检测和统计
- 📊 详细的性能统计信息
- 🔔 超时和慢请求回调通知

**使用示例：**
```go
timeoutController := timeout.NewTimeoutController(timeout.Config{
    Name:          "api-timeout",
    Timeout:       5 * time.Second,
    SlowThreshold: 2 * time.Second,
})

webserver.WithTimeout(timeoutController)
grpcserver.WithTimeout(timeoutController)
```

### 4. 熔断器中间件 (Circuit Breaker)

**核心特性：**
- 🔄 三种状态自动切换（关闭、开启、半开）
- 📈 可配置的失败阈值和恢复策略
- 📊 详细的熔断统计信息
- 🔔 状态变化回调通知

**使用示例：**
```go
circuitBreaker := circuitbreaker.NewCircuitBreaker(circuitbreaker.Config{
    Name:        "api-breaker",
    MaxRequests: 5,
    Interval:    time.Minute,
    Timeout:     30 * time.Second,
})

webserver.WithCircuitBreaker(circuitBreaker)
grpcserver.WithCircuitBreaker(circuitBreaker)
```

## 🔧 完整的使用示例

### HTTP 服务器
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
        log.Printf("Request: %s %s, Duration: %s", 
            r.Method, r.URL.Path, time.Since(start))
    })
}

// 创建应用
app := beauty.New(
    beauty.WithService(webserver.New(":8080", handler,
        webserver.WithServiceName("api-server"),
        // 中间件执行顺序（从外到内）：
        webserver.WithMiddleware(loggingMiddleware),  // 1. 日志记录
        webserver.WithAuth(authMiddleware),           // 2. 身份认证
        webserver.WithRateLimit(rateLimitMiddleware), // 3. 请求限流
        webserver.WithTimeout(timeoutController),    // 4. 超时控制
        webserver.WithCircuitBreaker(circuitBreaker), // 5. 熔断保护
    )),
)
```

### gRPC 服务器
```go
app := beauty.New(
    beauty.WithService(grpcserver.New(":9090", grpcHandler,
        grpcserver.WithServiceName("grpc-server"),
        // 拦截器执行顺序：
        grpcserver.WithAuth(authMiddleware),           // 1. 身份认证
        grpcserver.WithRateLimit(rateLimitMiddleware), // 2. 请求限流
        grpcserver.WithTimeout(timeoutController),    // 3. 超时控制
        grpcserver.WithCircuitBreaker(circuitBreaker), // 4. 熔断保护
    )),
)
```

## 🎛️ 业务自定义扩展

### 自定义认证器
```go
type MyAuthenticator struct {
    authService AuthService
}

func (a *MyAuthenticator) Authenticate(ctx context.Context, token string) (auth.User, error) {
    // 调用你的认证服务
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
    // 自定义键提取逻辑
    if tenantID, ok := metadata["tenant_id"].(string); ok {
        if userID, ok := metadata["user_id"].(string); ok {
            return fmt.Sprintf("tenant:%s:user:%s", tenantID, userID), nil
        }
        return "tenant:" + tenantID, nil
    }
    return "default", nil
}

// 使用自定义键提取器
rateLimitMiddleware := ratelimit.NewRateLimitMiddleware(ratelimit.Config{
    KeyExtractor: &MyKeyExtractor{},
})
```

## 📊 监控和管理

### 统一状态监控
```go
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

### 动态配置管理
```go
// 动态更新限流参数
mux.HandleFunc("/admin/ratelimit/update", func(w http.ResponseWriter, r *http.Request) {
    newRate := parseFloat(r.FormValue("rate"))
    newBurst := parseInt(r.FormValue("burst"))
    
    rateLimitMiddleware.UpdateRate(newRate, newBurst)
    w.Write([]byte("Rate limit updated"))
})

// 重置统计信息
mux.HandleFunc("/admin/stats/reset", func(w http.ResponseWriter, r *http.Request) {
    authMiddleware.ResetStats()
    rateLimitMiddleware.ResetStats()
    timeoutController.ResetStats()
    circuitBreaker.Reset()
    w.Write([]byte("Stats reset"))
})
```

## 🏆 设计优势

### 1. 灵活性
- ✅ 任意组合多个中间件
- ✅ 支持自定义中间件
- ✅ 可扩展的接口设计

### 2. 性能
- ✅ 中间件链在启动时构建，运行时开销最小
- ✅ 线程安全的实现
- ✅ 高效的令牌桶算法

### 3. 可观测性
- ✅ 详细的统计信息
- ✅ 状态变化回调
- ✅ 结构化日志记录

### 4. 易用性
- ✅ 统一的 API 设计
- ✅ 丰富的预定义实现
- ✅ 完善的文档和示例

### 5. 扩展性
- ✅ 基于接口的设计
- ✅ 支持业务自定义逻辑
- ✅ 向后兼容

## 🎯 总结

新的中间件系统完全解决了原有设计的局限性：

1. **解决了组合问题**：现在可以同时使用任意数量的中间件
2. **提供了强大的扩展能力**：业务方可以通过实现接口来自定义认证、授权、限流逻辑
3. **保持了一致的 API**：HTTP 和 gRPC 使用相同的设计模式
4. **提供了丰富的内置实现**：覆盖了常见的使用场景
5. **支持完整的监控**：详细的统计信息和状态监控

这个设计既满足了框架的灵活性要求，又为业务方提供了强大的自定义扩展能力。
