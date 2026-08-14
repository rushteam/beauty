# Beauty Microservice Framework Middleware System

This document describes the middleware system in the Beauty microservice framework, including usage of core middleware such as authentication, rate limiting, timeout control, and circuit breakers.

## Middleware Features

### Core Capabilities
- **Flexible Composition**: Combine multiple middleware in any order
- **High Performance**: Middleware chain is built at startup with minimal runtime overhead
- **Extensible**: Interface-based design supports custom extensions
- **Observable**: Provides detailed statistics and monitoring capabilities

### Built-in Middleware
- **Authentication Middleware**: Multiple authentication methods with flexible authorization
- **Rate Limit Middleware**: Multiple rate limiting strategies with dynamic parameter adjustment
- **Timeout Control**: Request timeout protection and slow-request monitoring
- **Circuit Breaker**: Fault isolation with automatic recovery

## Authentication Middleware

### Core Features
- **Multiple Token Extractors**: Header, Query, Cookie, gRPC Metadata, multi-source extractors
- **Extensible Authenticators**: Static tokens, JWT, callback authenticators, chained authenticators
- **Flexible Authorization**: Role-based, path-based, and callback authorizers
- **Complete Statistics**: Authentication success rate, failure counts, and more

### Basic Usage

```go
// Create authentication middleware
authMiddleware := auth.NewAuthMiddleware(auth.Config{
    Name: "api-auth",
    TokenExtractor: auth.NewHeaderTokenExtractor("Authorization", "Bearer "),
    Authenticator:  yourAuthenticator,
    SkipPaths:     []string{"/health", "/public"},
    EnableMetrics: true,
})

// Use in server
app := beauty.New(
    beauty.WithService(webserver.New(":8080", handler,
        webserver.WithAuth(authMiddleware),
    )),
)
```

## Rate Limit Middleware

### Core Features
- **Multiple Strategies**: IP-based, user-based, path-based, and custom key rate limiting
- **High-Performance Implementation**: Token bucket algorithm, thread-safe
- **Flexible Modes**: Direct reject mode and wait mode
- **Dynamic Adjustment**: Update rate limit parameters at runtime

### Basic Usage

```go
// Create rate limit middleware
rateLimitMiddleware := ratelimit.NewRateLimitMiddleware(ratelimit.Config{
    Name: "api-ratelimit", 
    Rate: 100.0, // 100 requests per second
    Burst: 200,  // Burst capacity 200
    KeyExtractor: ratelimit.NewIPKeyExtractor(),
    EnableMetrics: true,
})

// Use in server
app := beauty.New(
    beauty.WithService(webserver.New(":8080", handler,
        webserver.WithRateLimit(rateLimitMiddleware),     // Direct reject
        // or
        webserver.WithRateLimitWait(rateLimitMiddleware), // Wait to pass
    )),
)
```

## Timeout Control Middleware

### Core Features
- **Flexible Timeout Configuration**: Configurable timeout duration and slow-request threshold
- **Statistics and Monitoring**: Detailed timeout and performance statistics
- **Callback Notifications**: Callbacks for timeout and slow-request events
- **Context Propagation**: Correct handling of Go context cancellation

### Basic Usage

```go
// Create timeout controller
timeoutController := timeout.NewTimeoutController(timeout.Config{
    Name:          "api-timeout",
    Timeout:       5 * time.Second,
    SlowThreshold: 2 * time.Second,
    EnableMetrics: true,
    OnTimeout: func(name string, duration time.Duration) {
        logger.Warn("request timeout", "service", name, "duration", duration)
    },
})

// Use in server
app := beauty.New(
    beauty.WithService(webserver.New(":8080", handler,
        webserver.WithTimeout(timeoutController),
    )),
)
```

## Circuit Breaker Middleware

### Core Features
- **Three States**: Closed, open, and half-open with automatic transitions
- **Configurable Policy**: Failure threshold, recovery time, half-open request count
- **State Monitoring**: Detailed circuit breaker statistics and state change notifications
- **Automatic Recovery**: Intelligent fault recovery mechanism

### Basic Usage

```go
// Create circuit breaker
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

// Use in server
app := beauty.New(
    beauty.WithService(webserver.New(":8080", handler,
        webserver.WithCircuitBreaker(circuitBreaker),
    )),
)
```

## Middleware Composition

### Complete Middleware Stack

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
        logger.Info("request completed", 
            "path", r.URL.Path, 
            "duration", time.Since(start))
    })
}

// Web server — complete middleware stack
app := beauty.New(
    beauty.WithService(webserver.New(":8080", handler,
        webserver.WithServiceName("api-server"),
        webserver.WithMiddleware(loggingMiddleware),  // Custom middleware
        webserver.WithAuth(authMiddleware),           // Authentication
        webserver.WithRateLimit(rateLimitMiddleware), // Rate limiting
        webserver.WithTimeout(timeoutController),    // Timeout control
        webserver.WithCircuitBreaker(circuitBreaker), // Circuit breaker
    )),
)
```

### Middleware Execution Order

Middleware executes in the order it is added, forming an onion model:

```
Request -> Logging -> Auth -> Rate Limit -> Timeout -> Circuit Breaker -> Business Handler
         ↓          ↓      ↓           ↓              ↓
Response <- Logging <- Auth <- Rate Limit <- Timeout <- Circuit Breaker <- Business Handler
```

Recommended middleware order (outer to inner):
1. **Logging Middleware** — Log all requests
2. **Authentication Middleware** — Verify identity and permissions
3. **Rate Limit Middleware** — Control access frequency
4. **Timeout Control** — Prevent long-running blocking
5. **Circuit Breaker** — Prevent cascading failures
6. **Business Handler** — Actual business logic

## Monitoring and Statistics

### Retrieving Statistics
```go
// Authentication statistics
authStats := authMiddleware.Stats()
fmt.Printf("Authentication success rate: %.2f%%\n", authMiddleware.SuccessRate()*100)

// Rate limit statistics
rlStats := rateLimitMiddleware.Stats()
fmt.Printf("Rate limit rate: %.2f%%\n", 
    float64(rlStats.LimitedRequests)/float64(rlStats.TotalRequests)*100)

// Timeout statistics
tcStats := timeoutController.Stats()
fmt.Printf("Timeout rate: %.2f%%\n", timeoutController.TimeoutRate()*100)

// Circuit breaker statistics
cbStats := circuitBreaker.Counts()
fmt.Printf("Circuit breaker state: %s\n", circuitBreaker.State().String())
```

### Monitoring Endpoint
```go
// Unified status monitoring endpoint
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
