# Beauty Microservice Framework Middleware System Overview

## New Middleware Architecture

We redesigned the middleware system to address the inflexibility of the previous interface. It now supports composing any number of middleware together.

### Problems with the Old Design

```go
// Old design: each combination required a dedicated function
beauty.WithWebServerTimeout(":8080", handler, tc)
beauty.WithWebServerCircuitBreaker(":8080", handler, cb)
// Could not use multiple middleware at the same time!
```

### Advantages of the New Design

```go
// New design: flexibly compose any middleware
beauty.WithService(webserver.New(":8080", handler,
    webserver.WithAuth(authMiddleware),           // authentication
    webserver.WithRateLimit(rateLimitMiddleware), // rate limiting
    webserver.WithTimeout(timeoutController),    // timeout control
    webserver.WithCircuitBreaker(circuitBreaker), // circuit breaker
    webserver.WithMiddleware(customMiddleware),   // custom middleware
))
```

## Built-in Middleware

### 1. Authentication Middleware

**Core features:**
- Multiple token extraction methods (Header, Query, Cookie, gRPC Metadata)
- Extensible authenticator interface (static tokens, JWT, custom callbacks)
- Flexible authorization (roles, paths, custom rules)
- Detailed authentication statistics
- High performance and thread-safe

**Usage example:**

```go
// Create authentication middleware
authMiddleware := auth.NewAuthMiddleware(auth.Config{
    Name: "api-auth",
    TokenExtractor: auth.NewMultiTokenExtractor(
        auth.NewHeaderTokenExtractor("Authorization", "Bearer "),
        auth.NewQueryTokenExtractor("token"),
    ),
    Authenticator: yourCustomAuthenticator,
    SkipPaths:    []string{"/health", "/public"},
})

// Use in server
webserver.WithAuth(authMiddleware)
grpcserver.WithAuth(authMiddleware)
```

### 2. Rate Limit Middleware

**Core features:**
- Multiple rate limiting strategies (IP, user, path, custom keys)
- High-performance token bucket implementation
- Wait mode and direct reject mode
- Dynamic rate limit parameter adjustment
- Detailed rate limiting statistics

**Usage example:**

```go
// Create rate limit middleware
rateLimitMiddleware := ratelimit.NewRateLimitMiddleware(ratelimit.Config{
    Name: "api-ratelimit",
    Rate: 100.0, // 100 requests per second
    Burst: 200,  // burst capacity 200
    KeyExtractor: ratelimit.NewChainKeyExtractor(
        ratelimit.NewUserKeyExtractor("user_id"), // prefer per-user limiting
        ratelimit.NewIPKeyExtractor(),             // fall back to per-IP limiting
    ),
})

// Use in server
webserver.WithRateLimit(rateLimitMiddleware)      // direct reject
webserver.WithRateLimitWait(rateLimitMiddleware)  // wait to pass
grpcserver.WithRateLimit(rateLimitMiddleware)
```

### 3. Timeout Control Middleware

**Core features:**
- Flexible timeout configuration
- Slow request detection and statistics
- Detailed performance statistics
- Timeout and slow request callback notifications

**Usage example:**

```go
timeoutController := timeout.NewTimeoutController(timeout.Config{
    Name:          "api-timeout",
    Timeout:       5 * time.Second,
    SlowThreshold: 2 * time.Second,
})

webserver.WithTimeout(timeoutController)
grpcserver.WithTimeout(timeoutController)
```

### 4. Circuit Breaker Middleware

**Core features:**
- Automatic three-state transitions (closed, open, half-open)
- Configurable failure thresholds and recovery policies
- Detailed circuit breaker statistics
- State change callback notifications

**Usage example:**

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

## Complete Usage Examples

### HTTP Server

```go
// Create all middleware components
authMiddleware := createAuthMiddleware()
rateLimitMiddleware := createRateLimitMiddleware()
timeoutController := createTimeoutController()
circuitBreaker := createCircuitBreaker()

// Custom middleware
loggingMiddleware := func(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        next.ServeHTTP(w, r)
        log.Printf("Request: %s %s, Duration: %s", 
            r.Method, r.URL.Path, time.Since(start))
    })
}

// Create application
app := beauty.New(
    beauty.WithService(webserver.New(":8080", handler,
        webserver.WithServiceName("api-server"),
        // Middleware execution order (outer to inner):
        webserver.WithMiddleware(loggingMiddleware),  // 1. logging
        webserver.WithAuth(authMiddleware),           // 2. authentication
        webserver.WithRateLimit(rateLimitMiddleware), // 3. rate limiting
        webserver.WithTimeout(timeoutController),    // 4. timeout control
        webserver.WithCircuitBreaker(circuitBreaker), // 5. circuit breaker
    )),
)
```

### gRPC Server

```go
app := beauty.New(
    beauty.WithService(grpcserver.New(":9090", grpcHandler,
        grpcserver.WithServiceName("grpc-server"),
        // Interceptor execution order:
        grpcserver.WithAuth(authMiddleware),           // 1. authentication
        grpcserver.WithRateLimit(rateLimitMiddleware), // 2. rate limiting
        grpcserver.WithTimeout(timeoutController),    // 3. timeout control
        grpcserver.WithCircuitBreaker(circuitBreaker), // 4. circuit breaker
    )),
)
```

## Business Custom Extensions

### Custom Authenticator

```go
type MyAuthenticator struct {
    authService AuthService
}

func (a *MyAuthenticator) Authenticate(ctx context.Context, token string) (auth.User, error) {
    // Call your authentication service
    userInfo, err := a.authService.ValidateToken(token)
    if err != nil {
        return nil, auth.ErrInvalidToken
    }
    
    return auth.NewUser(userInfo.ID, userInfo.Name, userInfo.Roles), nil
}

// Use custom authenticator
authMiddleware := auth.NewAuthMiddleware(auth.Config{
    Authenticator: &MyAuthenticator{authService: yourAuthService},
})
```

### Custom Rate Limit Key Extractor

```go
type MyKeyExtractor struct{}

func (e *MyKeyExtractor) Extract(ctx context.Context, metadata map[string]interface{}) (string, error) {
    // Custom key extraction logic
    if tenantID, ok := metadata["tenant_id"].(string); ok {
        if userID, ok := metadata["user_id"].(string); ok {
            return fmt.Sprintf("tenant:%s:user:%s", tenantID, userID), nil
        }
        return "tenant:" + tenantID, nil
    }
    return "default", nil
}

// Use custom key extractor
rateLimitMiddleware := ratelimit.NewRateLimitMiddleware(ratelimit.Config{
    KeyExtractor: &MyKeyExtractor{},
})
```

## Monitoring and Management

### Unified Status Monitoring

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

### Dynamic Configuration Management

```go
// Dynamically update rate limit parameters
mux.HandleFunc("/admin/ratelimit/update", func(w http.ResponseWriter, r *http.Request) {
    newRate := parseFloat(r.FormValue("rate"))
    newBurst := parseInt(r.FormValue("burst"))
    
    rateLimitMiddleware.UpdateRate(newRate, newBurst)
    w.Write([]byte("Rate limit updated"))
})

// Reset statistics
mux.HandleFunc("/admin/stats/reset", func(w http.ResponseWriter, r *http.Request) {
    authMiddleware.ResetStats()
    rateLimitMiddleware.ResetStats()
    timeoutController.ResetStats()
    circuitBreaker.Reset()
    w.Write([]byte("Stats reset"))
})
```

## Design Advantages

### 1. Flexibility
- Compose any number of middleware together
- Support for custom middleware
- Extensible interface design

### 2. Performance
- Middleware chain built at startup with minimal runtime overhead
- Thread-safe implementations
- Efficient token bucket algorithm

### 3. Observability
- Detailed statistics
- State change callbacks
- Structured logging

### 4. Ease of Use
- Unified API design
- Rich predefined implementations
- Complete documentation and examples

### 5. Extensibility
- Interface-based design
- Support for business-specific custom logic
- Backward compatible

## Summary

The new middleware system fully addresses the limitations of the previous design:

1. **Solves composition problems**: Any number of middleware can now be used together
2. **Provides powerful extension capabilities**: Businesses can customize authentication, authorization, and rate limiting by implementing interfaces
3. **Maintains a consistent API**: HTTP and gRPC use the same design patterns
4. **Offers rich built-in implementations**: Covers common use cases
5. **Supports complete monitoring**: Detailed statistics and status monitoring

This design meets the framework's flexibility requirements while giving businesses powerful custom extension capabilities.
