# 认证和限流中间件 (Authentication & Rate Limiting Middleware)

本文档介绍 Beauty 微服务框架中的认证和限流中间件，以及如何灵活地组合使用它们。

## 🔐 认证中间件 (Authentication)

### 核心概念

认证中间件提供了灵活的认证和授权机制，支持多种认证方式和自定义扩展。

#### 主要组件

1. **TokenExtractor** - 令牌提取器：从请求中提取认证令牌
2. **Authenticator** - 认证器：验证令牌并返回用户信息
3. **Authorizer** - 授权器：检查用户权限
4. **User** - 用户接口：表示认证后的用户信息

### 基本使用

```go
// 创建认证器
authenticator := auth.NewStaticTokenAuthenticator()
authenticator.AddToken("admin-token", auth.NewUser("1", "admin", []string{"admin"}))

// 创建认证中间件
authConfig := auth.Config{
    Name: "api-auth",
    TokenExtractor: auth.NewHeaderTokenExtractor("Authorization", "Bearer "),
    Authenticator:  authenticator,
    SkipPaths:     []string{"/health", "/public"},
    EnableMetrics: true,
}
authMiddleware := auth.NewAuthMiddleware(authConfig)

// 使用认证中间件
app := beauty.New(
    beauty.WithService(webserver.New(":8080", handler,
        webserver.WithAuth(authMiddleware),
    )),
    beauty.WithService(grpcserver.New(":9090", grpcHandler,
        grpcserver.WithAuth(authMiddleware),
    )),
)
```

### 令牌提取器 (Token Extractors)

#### 1. Header 提取器
```go
// 从 Authorization header 提取 Bearer token
extractor := auth.NewHeaderTokenExtractor("Authorization", "Bearer ")

// 从自定义 header 提取
extractor := auth.NewHeaderTokenExtractor("X-API-Key", "")
```

#### 2. 查询参数提取器
```go
// 从 URL 查询参数提取
extractor := auth.NewQueryTokenExtractor("token")
// 访问：/api?token=your-token
```

#### 3. Cookie 提取器
```go
// 从 Cookie 提取
extractor := auth.NewCookieTokenExtractor("auth_token")
```

#### 4. 多源提取器
```go
// 按优先级尝试多个来源
extractor := auth.NewMultiTokenExtractor(
    auth.NewHeaderTokenExtractor("Authorization", "Bearer "),
    auth.NewQueryTokenExtractor("token"),
    auth.NewCookieTokenExtractor("auth_token"),
)
```

#### 5. gRPC Metadata 提取器
```go
// 从 gRPC metadata 提取
extractor := auth.NewGRPCMetadataExtractor("authorization")
```

### 认证器 (Authenticators)

#### 1. 静态令牌认证器
```go
authenticator := auth.NewStaticTokenAuthenticator()
authenticator.AddToken("token123", auth.NewUser("1", "john", []string{"user"}))
```

#### 2. JWT 认证器
```go
authenticator := auth.NewSimpleJWTAuthenticator("your-secret-key")
// 注意：这是简化实现，生产环境建议使用专业的 JWT 库
```

#### 3. 回调认证器（自定义认证逻辑）
```go
authenticator := auth.NewCallbackAuthenticator(func(ctx context.Context, token string) (auth.User, error) {
    // 自定义认证逻辑
    user, err := yourAuthService.ValidateToken(token)
    if err != nil {
        return nil, auth.ErrInvalidToken
    }
    return auth.NewUser(user.ID, user.Name, user.Roles), nil
})
```

#### 4. 链式认证器
```go
// 按顺序尝试多个认证器
authenticator := auth.NewChainAuthenticator(
    jwtAuthenticator,
    apiKeyAuthenticator,
    staticTokenAuthenticator,
)
```

### 授权器 (Authorizers)

#### 1. 基于角色的授权器
```go
authorizer := auth.NewRoleBasedAuthorizer()
authorizer.AddPermission("/api/users", "GET", "user", "admin")
authorizer.AddPermission("/api/admin", "POST", "admin")
```

#### 2. 基于路径的授权器
```go
authorizer := auth.NewPathBasedAuthorizer()
authorizer.AddPublicPath("/public/*")
authorizer.AddProtectedPath("/admin/*", []string{"admin"})
authorizer.AddProtectedPath("/api/*", []string{"user", "admin"})
```

#### 3. 回调授权器（自定义授权逻辑）
```go
authorizer := auth.NewCallbackAuthorizer(func(ctx context.Context, user auth.User, resource, action string) error {
    // 自定义授权逻辑
    if resource == "/admin" && !user.HasRole("admin") {
        return auth.ErrForbidden
    }
    return nil
})
```

## 🚦 限流中间件 (Rate Limiting)

### 核心概念

限流中间件基于令牌桶算法实现，支持多种限流策略和键提取方式。

### 基本使用

```go
// 创建限流中间件
config := ratelimit.Config{
    Name: "api-ratelimit",
    Rate: 100.0, // 每秒100个请求
    Burst: 200,  // 突发容量200
    KeyExtractor: ratelimit.NewIPKeyExtractor(),
    EnableMetrics: true,
}
rlMiddleware := ratelimit.NewRateLimitMiddleware(config)

// 使用限流中间件
app := beauty.New(
    beauty.WithService(webserver.New(":8080", handler,
        webserver.WithRateLimit(rlMiddleware),        // 直接拒绝
        // 或
        webserver.WithRateLimitWait(rlMiddleware),    // 等待通过
    )),
)
```

### 键提取器 (Key Extractors)

#### 1. IP 地址提取器
```go
// 按 IP 地址限流
extractor := ratelimit.NewIPKeyExtractor()
// 支持 X-Forwarded-For 和 X-Real-IP 头
```

#### 2. 用户提取器
```go
// 按用户 ID 限流
extractor := ratelimit.NewUserKeyExtractor("user_id")
```

#### 3. Header 提取器
```go
// 按 Header 值限流
extractor := ratelimit.NewHeaderKeyExtractor("X-API-Key", "api")
```

#### 4. 路径提取器
```go
// 按请求路径限流
extractor := ratelimit.NewPathKeyExtractor("path", true) // true: 去除查询参数
```

#### 5. 复合提取器
```go
// 组合多个键
extractor := ratelimit.NewCompositeKeyExtractor(":", "service",
    ratelimit.NewUserKeyExtractor("user_id"),
    ratelimit.NewPathKeyExtractor("path", true),
)
// 生成的键格式：service:user:123:path:/api/users
```

#### 6. 链式提取器
```go
// 按优先级尝试多个提取器
extractor := ratelimit.NewChainKeyExtractor(
    ratelimit.NewUserKeyExtractor("user_id"), // 优先按用户限流
    ratelimit.NewIPKeyExtractor(),             // 其次按 IP 限流
)
```

#### 7. 自定义提取器
```go
// 自定义键提取逻辑
extractor := ratelimit.NewCallbackKeyExtractor(func(ctx context.Context, metadata map[string]interface{}) (string, error) {
    // 自定义提取逻辑
    if userID, ok := metadata["user_id"].(string); ok {
        return "custom:" + userID, nil
    }
    return "default", nil
})
```

## 🔗 中间件组合使用

### 完整的中间件栈

```go
// 创建所有中间件组件
authMiddleware := createAuthMiddleware()
rateLimitMiddleware := createRateLimitMiddleware()
timeoutController := createTimeoutController()
circuitBreaker := createCircuitBreaker()

// 自定义日志中间件
loggingMiddleware := func(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        next.ServeHTTP(w, r)
        logger.Info("Request completed", 
            "path", r.URL.Path, 
            "duration", time.Since(start))
    })
}

// Web 服务器 - 中间件执行顺序很重要！
app := beauty.New(
    beauty.WithService(webserver.New(":8080", handler,
        webserver.WithServiceName("api-server"),
        // 执行顺序（从外到内）：
        webserver.WithMiddleware(loggingMiddleware),  // 1. 日志（最外层）
        webserver.WithAuth(authMiddleware),           // 2. 认证
        webserver.WithRateLimit(rateLimitMiddleware), // 3. 限流
        webserver.WithTimeout(timeoutController),    // 4. 超时控制
        webserver.WithCircuitBreaker(circuitBreaker), // 5. 熔断器（最内层）
    )),
)
```

### 中间件执行流程

```
请求进入 -> 日志中间件 -> 认证中间件 -> 限流中间件 -> 超时控制 -> 熔断器 -> 业务处理器
                ↓             ↓            ↓           ↓         ↓
             记录开始      验证身份      检查限流     设置超时    检查熔断
                ↑             ↑            ↑           ↑         ↑
响应返回 <- 记录结束   <- 授权检查   <- 更新计数  <- 记录耗时  <- 更新状态
```

### 不同场景的配置

#### 1. 公开 API（无认证，有限流）
```go
beauty.WithService(webserver.New(":8080", handler,
    webserver.WithRateLimit(rateLimitMiddleware),
    webserver.WithTimeout(timeoutController),
))
```

#### 2. 内部 API（有认证，无限流）
```go
beauty.WithService(webserver.New(":8080", handler,
    webserver.WithAuth(authMiddleware),
    webserver.WithTimeout(timeoutController),
))
```

#### 3. 高可用 API（全套保护）
```go
beauty.WithService(webserver.New(":8080", handler,
    webserver.WithAuth(authMiddleware),
    webserver.WithRateLimit(rateLimitMiddleware),
    webserver.WithTimeout(timeoutController),
    webserver.WithCircuitBreaker(circuitBreaker),
))
```

## 📊 监控和统计

### 认证统计
```go
stats := authMiddleware.Stats()
fmt.Printf("认证成功率: %.2f%%\n", authMiddleware.SuccessRate()*100)
fmt.Printf("总请求: %d, 成功: %d, 失败: %d\n", 
    stats.TotalRequests, stats.SuccessRequests, stats.FailureRequests)
```

### 限流统计
```go
stats := rateLimitMiddleware.Stats()
fmt.Printf("限流率: %.2f%%\n", 
    float64(stats.LimitedRequests)/float64(stats.TotalRequests)*100)
fmt.Printf("活跃限流器: %d\n", stats.ActiveLimiters)
```

### HTTP 监控端点
```go
mux.HandleFunc("/middleware/status", func(w http.ResponseWriter, r *http.Request) {
    response := map[string]interface{}{
        "auth": authMiddleware.Stats(),
        "rate_limit": rateLimitMiddleware.Stats(),
        "timeout": timeoutController.Stats(),
        "circuit_breaker": circuitBreaker.Counts(),
    }
    json.NewEncoder(w).Encode(response)
})
```

## 🛠️ 自定义扩展

### 自定义认证器
```go
type CustomAuthenticator struct {
    // 你的认证逻辑
}

func (a *CustomAuthenticator) Authenticate(ctx context.Context, token string) (auth.User, error) {
    // 实现你的认证逻辑
    // 例如：调用外部认证服务、验证数据库等
    user, err := yourAuthService.Validate(token)
    if err != nil {
        return nil, auth.ErrInvalidToken
    }
    return auth.NewUser(user.ID, user.Name, user.Roles), nil
}
```

### 自定义授权器
```go
type CustomAuthorizer struct {
    // 你的授权逻辑
}

func (a *CustomAuthorizer) Authorize(ctx context.Context, user auth.User, resource, action string) error {
    // 实现你的授权逻辑
    // 例如：检查用户权限、调用权限服务等
    if !yourPermissionService.CheckPermission(user.ID(), resource, action) {
        return auth.ErrForbidden
    }
    return nil
}
```

### 自定义键提取器
```go
type CustomKeyExtractor struct {
    // 你的键提取逻辑
}

func (e *CustomKeyExtractor) Extract(ctx context.Context, metadata map[string]interface{}) (string, error) {
    // 实现你的键提取逻辑
    // 例如：根据业务规则生成限流键
    if tenantID, ok := metadata["tenant_id"].(string); ok {
        return "tenant:" + tenantID, nil
    }
    return "default", nil
}
```

## 💡 最佳实践

### 1. 中间件顺序
推荐的中间件执行顺序：
1. **日志中间件** - 记录所有请求
2. **认证中间件** - 验证身份
3. **限流中间件** - 控制访问频率
4. **超时控制** - 防止长时间阻塞
5. **熔断器** - 防止级联故障

### 2. 认证策略
- **公开端点**：使用 `SkipPaths` 配置跳过认证
- **可选认证**：使用 `auth.OptionalAuth()` 中间件
- **强制认证**：使用标准的认证中间件
- **角色检查**：使用 `auth.RequireRole()` 中间件

### 3. 限流策略
- **全局限流**：使用默认键
- **用户限流**：使用用户键提取器
- **IP 限流**：使用 IP 键提取器
- **混合限流**：使用复合键提取器

### 4. 错误处理
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

## 📝 完整示例

```go
package main

import (
    "context"
    "net/http"
    "time"
    
    "github.com/rushteam/beauty"
    "github.com/rushteam/beauty/pkg/auth"
    "github.com/rushteam/beauty/pkg/ratelimit"
    "github.com/rushteam/beauty/pkg/service/webserver"
)

func main() {
    // 1. 创建认证组件
    authenticator := auth.NewCallbackAuthenticator(func(ctx context.Context, token string) (auth.User, error) {
        // 你的认证逻辑
        return yourAuthService.Authenticate(token)
    })
    
    authMiddleware := auth.NewAuthMiddleware(auth.Config{
        Name: "api-auth",
        TokenExtractor: auth.NewMultiTokenExtractor(
            auth.NewHeaderTokenExtractor("Authorization", "Bearer "),
            auth.NewQueryTokenExtractor("token"),
        ),
        Authenticator: authenticator,
        SkipPaths:     []string{"/health", "/public"},
    })

    // 2. 创建限流组件
    rateLimitMiddleware := ratelimit.NewRateLimitMiddleware(ratelimit.Config{
        Name: "api-ratelimit",
        Rate: 100.0, // 每秒100个请求
        Burst: 200,
        KeyExtractor: ratelimit.NewChainKeyExtractor(
            ratelimit.NewUserKeyExtractor("user_id"),
            ratelimit.NewIPKeyExtractor(),
        ),
    })

    // 3. 创建路由
    mux := http.NewServeMux()
    mux.HandleFunc("/api/users", usersHandler)
    mux.HandleFunc("/public", publicHandler)

    // 4. 创建应用
    app := beauty.New(
        beauty.WithService(webserver.New(":8080", mux,
            webserver.WithAuth(authMiddleware),
            webserver.WithRateLimit(rateLimitMiddleware),
        )),
    )

    app.Start(context.Background())
}
```

## 🔍 调试和故障排除

### 启用详细日志
```go
authConfig.OnAuthSuccess = func(ctx context.Context, user auth.User) {
    logger.Info("Authentication successful", "user_id", user.ID())
}
authConfig.OnAuthFailure = func(ctx context.Context, err error) {
    logger.Warn("Authentication failed", "error", err)
}

rateLimitConfig.OnRateLimit = func(ctx context.Context, key string, rate float64) {
    logger.Warn("Rate limit exceeded", "key", key, "rate", rate)
}
```

### 监控端点
```go
mux.HandleFunc("/debug/auth", func(w http.ResponseWriter, r *http.Request) {
    json.NewEncoder(w).Encode(authMiddleware.Stats())
})

mux.HandleFunc("/debug/ratelimit", func(w http.ResponseWriter, r *http.Request) {
    json.NewEncoder(w).Encode(rateLimitMiddleware.Stats())
})
```

这个设计提供了强大的扩展能力，业务方可以通过实现相应的接口来自定义认证、授权和限流逻辑，同时保持了框架的一致性和易用性。
