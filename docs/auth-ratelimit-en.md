# Authentication and Rate Limiting Middleware

This document describes the authentication and rate limiting middleware in the Beauty microservice framework, and how to compose them flexibly.

## Authentication

### Core Concepts

The authentication middleware provides flexible authentication and authorization, supporting multiple auth methods and custom extensions.

#### Main Components

1. **TokenExtractor** — extracts authentication tokens from requests
2. **Authenticator** — validates tokens and returns user information
3. **Authorizer** — checks user permissions
4. **User** — interface representing authenticated user information

### Basic Usage

```go
// Create authenticator
authenticator := auth.NewStaticTokenAuthenticator()
authenticator.AddToken("admin-token", auth.NewUser("1", "admin", []string{"admin"}))

// Create auth middleware
authConfig := auth.Config{
    Name: "api-auth",
    TokenExtractor: auth.NewHeaderTokenExtractor("Authorization", "Bearer "),
    Authenticator:  authenticator,
    SkipPaths:     []string{"/health", "/public"},
    EnableMetrics: true,
}
authMiddleware := auth.NewAuthMiddleware(authConfig)

// Use auth middleware
app := beauty.New(
    beauty.WithService(webserver.New(":8080", handler,
        webserver.WithAuth(authMiddleware),
    )),
    beauty.WithService(grpcserver.New(":9090", grpcHandler,
        grpcserver.WithAuth(authMiddleware),
    )),
)
```

### Token Extractors

#### 1. Header Extractor
```go
// Extract Bearer token from Authorization header
extractor := auth.NewHeaderTokenExtractor("Authorization", "Bearer ")

// Extract from custom header
extractor := auth.NewHeaderTokenExtractor("X-API-Key", "")
```

#### 2. Query Parameter Extractor
```go
// Extract from URL query parameter
extractor := auth.NewQueryTokenExtractor("token")
// Access: /api?token=your-token
```

#### 3. Cookie Extractor
```go
// Extract from Cookie
extractor := auth.NewCookieTokenExtractor("auth_token")
```

#### 4. Multi-Source Extractor
```go
// Try multiple sources in priority order
extractor := auth.NewMultiTokenExtractor(
    auth.NewHeaderTokenExtractor("Authorization", "Bearer "),
    auth.NewQueryTokenExtractor("token"),
    auth.NewCookieTokenExtractor("auth_token"),
)
```

#### 5. gRPC Metadata Extractor
```go
// Extract from gRPC metadata
extractor := auth.NewGRPCMetadataExtractor("authorization")
```

### Authenticators

#### 1. Static Token Authenticator
```go
authenticator := auth.NewStaticTokenAuthenticator()
authenticator.AddToken("token123", auth.NewUser("1", "john", []string{"user"}))
```

#### 2. JWT Authenticator
```go
authenticator := auth.NewSimpleJWTAuthenticator("your-secret-key")
// Note: this is a simplified implementation; use a production-grade JWT library in production
```

#### 3. Callback Authenticator (custom auth logic)
```go
authenticator := auth.NewCallbackAuthenticator(func(ctx context.Context, token string) (auth.User, error) {
    // Custom authentication logic
    user, err := yourAuthService.ValidateToken(token)
    if err != nil {
        return nil, auth.ErrInvalidToken
    }
    return auth.NewUser(user.ID, user.Name, user.Roles), nil
})
```

#### 4. Chain Authenticator
```go
// Try multiple authenticators in order
authenticator := auth.NewChainAuthenticator(
    jwtAuthenticator,
    apiKeyAuthenticator,
    staticTokenAuthenticator,
)
```

### Authorizers

#### 1. Role-Based Authorizer
```go
authorizer := auth.NewRoleBasedAuthorizer()
authorizer.AddPermission("/api/users", "GET", "user", "admin")
authorizer.AddPermission("/api/admin", "POST", "admin")
```

#### 2. Path-Based Authorizer
```go
authorizer := auth.NewPathBasedAuthorizer()
authorizer.AddPublicPath("/public/*")
authorizer.AddProtectedPath("/admin/*", []string{"admin"})
authorizer.AddProtectedPath("/api/*", []string{"user", "admin"})
```

#### 3. Callback Authorizer (custom authorization logic)
```go
authorizer := auth.NewCallbackAuthorizer(func(ctx context.Context, user auth.User, resource, action string) error {
    // Custom authorization logic
    if resource == "/admin" && !user.HasRole("admin") {
        return auth.ErrForbidden
    }
    return nil
})
```

## Rate Limiting

### Core Concepts

The rate limiting middleware is based on the token bucket algorithm, supporting multiple rate limiting strategies and key extraction methods.

### Basic Usage

```go
// Create rate limit middleware
config := ratelimit.Config{
    Name: "api-ratelimit",
    Rate: 100.0, // 100 requests per second
    Burst: 200,  // burst capacity 200
    KeyExtractor: ratelimit.NewIPKeyExtractor(),
    EnableMetrics: true,
}
rlMiddleware := ratelimit.NewRateLimitMiddleware(config)

// Use rate limit middleware
app := beauty.New(
    beauty.WithService(webserver.New(":8080", handler,
        webserver.WithRateLimit(rlMiddleware),        // reject immediately
        // or
        webserver.WithRateLimitWait(rlMiddleware),    // wait until allowed
    )),
)
```

### Key Extractors

#### 1. IP Address Extractor
```go
// Rate limit by IP address
extractor := ratelimit.NewIPKeyExtractor()
// Supports X-Forwarded-For and X-Real-IP headers
```

#### 2. User Extractor
```go
// Rate limit by user ID
extractor := ratelimit.NewUserKeyExtractor("user_id")
```

#### 3. Header Extractor
```go
// Rate limit by header value
extractor := ratelimit.NewHeaderKeyExtractor("X-API-Key", "api")
```

#### 4. Path Extractor
```go
// Rate limit by request path
extractor := ratelimit.NewPathKeyExtractor("path", true) // true: strip query parameters
```

#### 5. Composite Extractor
```go
// Combine multiple keys
extractor := ratelimit.NewCompositeKeyExtractor(":", "service",
    ratelimit.NewUserKeyExtractor("user_id"),
    ratelimit.NewPathKeyExtractor("path", true),
)
// Generated key format: service:user:123:path:/api/users
```

#### 6. Chain Extractor
```go
// Try multiple extractors in priority order
extractor := ratelimit.NewChainKeyExtractor(
    ratelimit.NewUserKeyExtractor("user_id"), // prefer user-based limiting
    ratelimit.NewIPKeyExtractor(),             // fall back to IP-based limiting
)
```

#### 7. Custom Extractor
```go
// Custom key extraction logic
extractor := ratelimit.NewCallbackKeyExtractor(func(ctx context.Context, metadata map[string]interface{}) (string, error) {
    // Custom extraction logic
    if userID, ok := metadata["user_id"].(string); ok {
        return "custom:" + userID, nil
    }
    return "default", nil
})
```

## Composing Middleware

### Full Middleware Stack

```go
// Create all middleware components
authMiddleware := createAuthMiddleware()
rateLimitMiddleware := createRateLimitMiddleware()
timeoutController := createTimeoutController()
circuitBreaker := createCircuitBreaker()

// Custom logging middleware
loggingMiddleware := func(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        next.ServeHTTP(w, r)
        logger.Info("Request completed", 
            "path", r.URL.Path, 
            "duration", time.Since(start))
    })
}

// Web server — middleware execution order matters!
app := beauty.New(
    beauty.WithService(webserver.New(":8080", handler,
        webserver.WithServiceName("api-server"),
        // Execution order (outer to inner):
        webserver.WithMiddleware(loggingMiddleware),  // 1. logging (outermost)
        webserver.WithAuth(authMiddleware),           // 2. authentication
        webserver.WithRateLimit(rateLimitMiddleware), // 3. rate limiting
        webserver.WithTimeout(timeoutController),    // 4. timeout control
        webserver.WithCircuitBreaker(circuitBreaker), // 5. circuit breaker (innermost)
    )),
)
```

### Middleware Execution Flow

```
Request in -> Logging -> Auth -> Rate Limit -> Timeout -> Circuit Breaker -> Business Handler
                ↓          ↓         ↓           ↓            ↓
             Log start  Verify   Check limit  Set timeout  Check breaker
                ↑          ↑         ↑           ↑            ↑
Response out <- Log end <- Authz <- Update count <- Record duration <- Update state
```

### Configuration for Different Scenarios

#### 1. Public API (no auth, with rate limiting)
```go
beauty.WithService(webserver.New(":8080", handler,
    webserver.WithRateLimit(rateLimitMiddleware),
    webserver.WithTimeout(timeoutController),
))
```

#### 2. Internal API (with auth, no rate limiting)
```go
beauty.WithService(webserver.New(":8080", handler,
    webserver.WithAuth(authMiddleware),
    webserver.WithTimeout(timeoutController),
))
```

#### 3. High-Availability API (full protection)
```go
beauty.WithService(webserver.New(":8080", handler,
    webserver.WithAuth(authMiddleware),
    webserver.WithRateLimit(rateLimitMiddleware),
    webserver.WithTimeout(timeoutController),
    webserver.WithCircuitBreaker(circuitBreaker),
))
```

## Monitoring and Metrics

### Authentication Stats
```go
stats := authMiddleware.Stats()
fmt.Printf("Auth success rate: %.2f%%\n", authMiddleware.SuccessRate()*100)
fmt.Printf("Total: %d, Success: %d, Failure: %d\n", 
    stats.TotalRequests, stats.SuccessRequests, stats.FailureRequests)
```

### Rate Limiting Stats
```go
stats := rateLimitMiddleware.Stats()
fmt.Printf("Rate limit rate: %.2f%%\n", 
    float64(stats.LimitedRequests)/float64(stats.TotalRequests)*100)
fmt.Printf("Active limiters: %d\n", stats.ActiveLimiters)
```

### HTTP Monitoring Endpoint
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

## Custom Extensions

### Custom Authenticator
```go
type CustomAuthenticator struct {
    // Your authentication logic
}

func (a *CustomAuthenticator) Authenticate(ctx context.Context, token string) (auth.User, error) {
    // Implement your authentication logic
    // e.g. call external auth service, validate against database, etc.
    user, err := yourAuthService.Validate(token)
    if err != nil {
        return nil, auth.ErrInvalidToken
    }
    return auth.NewUser(user.ID, user.Name, user.Roles), nil
}
```

### Custom Authorizer
```go
type CustomAuthorizer struct {
    // Your authorization logic
}

func (a *CustomAuthorizer) Authorize(ctx context.Context, user auth.User, resource, action string) error {
    // Implement your authorization logic
    // e.g. check user permissions, call permission service, etc.
    if !yourPermissionService.CheckPermission(user.ID(), resource, action) {
        return auth.ErrForbidden
    }
    return nil
}
```

### Custom Key Extractor
```go
type CustomKeyExtractor struct {
    // Your key extraction logic
}

func (e *CustomKeyExtractor) Extract(ctx context.Context, metadata map[string]interface{}) (string, error) {
    // Implement your key extraction logic
    // e.g. generate rate limit key based on business rules
    if tenantID, ok := metadata["tenant_id"].(string); ok {
        return "tenant:" + tenantID, nil
    }
    return "default", nil
}
```

## Multi-Tenant Rate Limiting

Combine `pkg/middleware/tenant` with `TenantKeyExtractor` for per-tenant quotas in SaaS APIs.

**Recommended middleware order:** `propagation.HTTPServerMiddleware` → `tenant.HTTPMiddleware()` → `ratelimit.HTTPMiddleware(...)`.

```go
import (
    "github.com/rushteam/beauty/pkg/middleware/ratelimit"
    "github.com/rushteam/beauty/pkg/middleware/tenant"
    "github.com/rushteam/beauty/pkg/metadata/propagation"
)

rl := ratelimit.NewRateLimitMiddleware(ratelimit.Config{
    Name:  "api-ratelimit",
    Rate:  100,
    Burst: 200,
    KeyExtractor: ratelimit.NewTenantKeyExtractor(),
    RateOverride: func(key string) (float64, int, bool) {
        switch key {
        case "tenant:enterprise":
            return 1000, 2000, true
        case "tenant:free":
            return 10, 20, true
        }
        return 0, 0, false
    },
})

webserver.WithMiddleware(
    propagation.HTTPServerMiddleware,
    tenant.HTTPMiddleware(),
    ratelimit.HTTPMiddleware(rl),
)
```

Clients send `X-Tenant-ID` (or gRPC metadata `x-tenant-id`). `TenantKeyExtractor` reads `tenant.FromContext` first, then falls back to headers.

For idempotency key design see [`docs/idempotency.md`](idempotency.md); for geo routing see [`docs/geo-routing.md`](geo-routing.md).

## Best Practices

### 1. Middleware Order
Recommended middleware execution order:
1. **Logging middleware** — record all requests
2. **Authentication middleware** — verify identity
3. **Rate limiting middleware** — control access frequency
4. **Timeout control** — prevent long blocking
5. **Circuit breaker** — prevent cascading failures

### 2. Authentication Strategy
- **Public endpoints**: use `SkipPaths` to skip authentication
- **Optional authentication**: use `auth.OptionalAuth()` middleware
- **Required authentication**: use the standard auth middleware
- **Role checks**: use `auth.RequireRole()` middleware

### 3. Rate Limiting Strategy
- **Global rate limiting**: use the default key
- **Per-user rate limiting**: use the user key extractor
- **Per-IP rate limiting**: use the IP key extractor
- **Mixed rate limiting**: use the composite key extractor

### 4. Error Handling
```go
// Check authentication errors
if auth.IsAuthError(err) {
    // handle auth error
}

// Check rate limit errors
if ratelimit.IsRateLimitError(err) {
    // handle rate limit error
}

// Get user information
if user, ok := auth.GetUserFromContext(ctx); ok {
    // use user information
}
```

## Complete Example

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
    // 1. Create auth components
    authenticator := auth.NewCallbackAuthenticator(func(ctx context.Context, token string) (auth.User, error) {
        // Your authentication logic
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

    // 2. Create rate limit components
    rateLimitMiddleware := ratelimit.NewRateLimitMiddleware(ratelimit.Config{
        Name: "api-ratelimit",
        Rate: 100.0, // 100 requests per second
        Burst: 200,
        KeyExtractor: ratelimit.NewChainKeyExtractor(
            ratelimit.NewUserKeyExtractor("user_id"),
            ratelimit.NewIPKeyExtractor(),
        ),
    })

    // 3. Create routes
    mux := http.NewServeMux()
    mux.HandleFunc("/api/users", usersHandler)
    mux.HandleFunc("/public", publicHandler)

    // 4. Create application
    app := beauty.New(
        beauty.WithService(webserver.New(":8080", mux,
            webserver.WithAuth(authMiddleware),
            webserver.WithRateLimit(rateLimitMiddleware),
        )),
    )

    app.Start(context.Background())
}
```

## Debugging and Troubleshooting

### Enable Verbose Logging
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

### Monitoring Endpoints
```go
mux.HandleFunc("/debug/auth", func(w http.ResponseWriter, r *http.Request) {
    json.NewEncoder(w).Encode(authMiddleware.Stats())
})

mux.HandleFunc("/debug/ratelimit", func(w http.ResponseWriter, r *http.Request) {
    json.NewEncoder(w).Encode(rateLimitMiddleware.Stats())
})
```

This design provides strong extensibility—applications can customize authentication, authorization, and rate limiting logic by implementing the corresponding interfaces, while maintaining framework consistency and ease of use.
