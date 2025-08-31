# Beauty 框架中间件

本目录包含了 Beauty 微服务框架的所有中间件实现，提供了统一的中间件管理和使用方式。

## 📁 中间件目录

### 🔐 认证中间件 (`auth/`)
提供完整的认证和授权功能：
- **多种令牌提取器**：Header、Query、Cookie、gRPC Metadata、多源提取器
- **可扩展认证器**：静态令牌、JWT、回调认证器、链式认证器  
- **灵活授权机制**：基于角色、基于路径、回调授权器
- **完整统计信息**：认证成功率、失败统计等

```go
import "github.com/rushteam/beauty/pkg/middleware/auth"

authMiddleware := auth.NewAuthMiddleware(auth.Config{
    TokenExtractor: auth.NewHeaderTokenExtractor("Authorization", "Bearer "),
    Authenticator:  yourAuthenticator,
    SkipPaths:     []string{"/health", "/public"},
})
```

### 🚦 限流中间件 (`ratelimit/`)
基于令牌桶算法的高性能限流：
- **多种限流策略**：IP 限流、用户限流、路径限流、自定义键
- **高性能实现**：基于令牌桶算法，线程安全
- **灵活模式**：直接拒绝模式和等待模式
- **动态调整**：运行时更新限流参数

```go
import "github.com/rushteam/beauty/pkg/middleware/ratelimit"

rateLimitMiddleware := ratelimit.NewRateLimitMiddleware(ratelimit.Config{
    Rate: 100.0, // 每秒100个请求
    Burst: 200,  // 突发容量200
    KeyExtractor: ratelimit.NewIPKeyExtractor(),
})
```

### ⏱️ 超时控制中间件 (`timeout/`)
请求超时保护和慢请求监控：
- **灵活超时配置**：可配置超时时间和慢请求阈值
- **统计监控**：详细的超时和性能统计
- **回调通知**：超时和慢请求事件回调
- **上下文传递**：正确处理 Go context 取消

```go
import "github.com/rushteam/beauty/pkg/middleware/timeout"

timeoutController := timeout.NewTimeoutController(timeout.Config{
    Timeout:       5 * time.Second,
    SlowThreshold: 2 * time.Second,
})
```

### 🔄 熔断器中间件 (`circuitbreaker/`)
故障隔离和自动恢复机制：
- **三种状态**：关闭、开启、半开状态自动切换
- **可配置策略**：失败阈值、恢复时间、半开请求数
- **状态监控**：详细的熔断统计和状态变化通知
- **自动恢复**：智能的故障恢复机制

```go
import "github.com/rushteam/beauty/pkg/middleware/circuitbreaker"

circuitBreaker := circuitbreaker.NewCircuitBreaker(circuitbreaker.Config{
    MaxRequests: 5,
    Interval:    time.Minute,
    Timeout:     30 * time.Second,
})
```

## 🔗 中间件组合使用

所有中间件都支持灵活组合，可以根据业务需求任意搭配：

```go
// HTTP 服务器 - 完整中间件栈
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

// gRPC 服务器 - 完整拦截器栈
app := beauty.New(
    beauty.WithService(grpcserver.New(":9090", grpcHandler,
        grpcserver.WithServiceName("grpc-server"),
        grpcserver.WithAuth(authMiddleware),           // 认证
        grpcserver.WithRateLimit(rateLimitMiddleware), // 限流
        grpcserver.WithTimeout(timeoutController),    // 超时控制
        grpcserver.WithCircuitBreaker(circuitBreaker), // 熔断器
    )),
)
```

## 📊 中间件执行顺序

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

## 🛠️ 自定义扩展

所有中间件都基于接口设计，支持业务方自定义扩展：

### 自定义认证器
```go
type MyAuthenticator struct {
    authService AuthService
}

func (a *MyAuthenticator) Authenticate(ctx context.Context, token string) (auth.User, error) {
    return a.authService.ValidateToken(token)
}
```

### 自定义限流键提取器
```go
type MyKeyExtractor struct{}

func (e *MyKeyExtractor) Extract(ctx context.Context, metadata map[string]interface{}) (string, error) {
    return "custom-key", nil
}
```

### 自定义中间件
```go
func customMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // 前置处理
        next.ServeHTTP(w, r)
        // 后置处理
    })
}
```

## 📈 监控和管理

所有中间件都提供统计信息和管理接口：

```go
// 获取统计信息
authStats := authMiddleware.Stats()
rateLimitStats := rateLimitMiddleware.Stats()
timeoutStats := timeoutController.Stats()
circuitBreakerStats := circuitBreaker.Counts()

// 重置统计信息
authMiddleware.ResetStats()
rateLimitMiddleware.ResetStats()
timeoutController.ResetStats()
circuitBreaker.Reset()

// 动态配置调整
rateLimitMiddleware.UpdateRate(newRate, newBurst)
```

## 🎯 最佳实践

### 1. 合理配置参数
- **超时时间**：根据业务场景设置合理的超时时间
- **限流速率**：根据系统容量和业务需求配置限流参数
- **熔断阈值**：根据服务稳定性要求设置熔断参数

### 2. 监控和告警
- 定期检查中间件统计信息
- 设置合适的监控告警阈值
- 建立故障处理流程

### 3. 测试验证
- 进行压力测试验证中间件效果
- 模拟故障场景测试熔断器
- 验证认证和授权逻辑

### 4. 配置管理
- 使用配置文件管理中间件参数
- 支持动态配置更新
- 建立配置变更审计

## 📚 相关文档

- [中间件系统文档](../../docs/middleware.md)
- [认证和限流详细文档](../../docs/auth-ratelimit.md)
- [示例代码](../../example/)

## 🔍 故障排除

### 运行时问题
1. 检查中间件配置是否正确
2. 查看统计信息了解运行状态
3. 检查日志获取详细错误信息
4. 验证中间件执行顺序是否合理

### 性能问题
1. 调整中间件参数（超时时间、限流速率等）
2. 查看监控端点了解系统状态
3. 检查日志了解详细信息
4. 考虑是否需要调整中间件顺序
