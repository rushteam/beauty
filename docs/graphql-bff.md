# GraphQL/BFF Layer — contrib/graphql

`contrib/graphql` is a standalone Go module (`github.com/rushteam/beauty/contrib/graphql`) that wraps [gqlgen](https://github.com/99designs/gqlgen) (schema-first) as a beauty first-class `Service`, providing production-grade BFF (Backend for Frontend) aggregation capabilities.

## Architecture

```
Client (Web/Mobile)
    │
    ▼  HTTP / WebSocket
┌───────────────────────────────────────────────┐
│  contrib/graphql.Server (beauty.Service)       │
│                                                │
│  ┌─────────┐ ┌──────────┐ ┌───────────────┐  │
│  │ Auth    │ │Complexity│ │ APQ / White   │  │
│  │Extractor│ │  Limiter │ │   list        │  │
│  └────┬────┘ └────┬─────┘ └───────┬───────┘  │
│       └────────────┴───────────────┘          │
│                    │                           │
│  ┌─────────────────▼──────────────────┐       │
│  │     Resolver Layer                  │       │
│  │  ┌──────────┐  ┌────────────────┐  │       │
│  │  │DataLoader│  │ grpcclient /   │  │       │
│  │  │ (batch)  │  │ httpclient     │  │       │
│  │  └──────────┘  └────────────────┘  │       │
│  └─────────────────────────────────────┘       │
│                                                │
│  Subscription: WebSocket + SSE                 │
└───────────────────────────────────────────────┘
    │                    │
    ▼                    ▼
  gRPC backends      HTTP backends
```

## Quick Start

```go
import (
    "github.com/rushteam/beauty"
    gql "github.com/rushteam/beauty/contrib/graphql"
    "github.com/rushteam/beauty/contrib/graphql/complexity"
    "github.com/rushteam/beauty/contrib/graphql/subscription"
    "your-project/graph/generated"
)

func main() {
    es := generated.NewExecutableSchema(generated.Config{
        Resolvers: &resolver{},
    })

    app := beauty.New(
        beauty.WithService(gql.New(":8080", es,
            gql.WithName("my-bff"),
            gql.WithPlayground(true),
            gql.WithExtension(complexity.Extension(complexity.Config{
                MaxComplexity: 200,
                MaxDepth:      10,
            })),
            gql.WithTransport(subscription.WSTransport()),
            gql.WithTransport(subscription.SSETransport()),
        )),
        beauty.WithTrace(...),
    )
    app.Start(context.Background())
}
```

## Packages

### `contrib/graphql` (root)

The `Server` type wraps a gqlgen `ExecutableSchema` into a `beauty.Service`:

- Implements `Start(ctx) error`, `String()`, `Ready()`, `Kind()`, `Addr()`, `Metadata()`
- Registers with service discovery as `kind: http, protocol: graphql`
- Playground UI at configurable path
- Middleware stack (HTTP level) applied in order

### `contrib/graphql/dataloader`

Generic DataLoader for N+1 query batching with per-request lifecycle caching.

```go
// Define a batch function
func batchUsers(ctx context.Context, ids []string) (map[string]*User, error) {
    return userService.GetByIDs(ctx, ids)
}

// Create loader (per-request instance via middleware)
loader := dataloader.NewLoader(batchUsers,
    dataloader.WithBatchSize(100),
    dataloader.WithBatchWait(2*time.Millisecond),
)

// Use in resolver
user, err := loader.Load(ctx, userID)
users, err := loader.LoadMany(ctx, userIDs)
```

The `Middleware` function creates per-request loader instances:

```go
mux.Handle("/query", dataloader.Middleware(func() *dataloader.Registry {
    reg := &dataloader.Registry{}
    dataloader.Register(reg, "users", dataloader.NewLoader(batchUsers))
    return reg
})(graphqlHandler))

// In resolver:
loader, _ := dataloader.Get[string, *User](ctx, "users")
user, _ := loader.Load(ctx, id)
```

### `contrib/graphql/complexity`

Query complexity and depth limiting as a gqlgen `HandlerExtension`:

```go
ext := complexity.Extension(complexity.Config{
    MaxComplexity: 200,
    MaxDepth:      15,
    FieldWeights: map[string]int{
        "Query.users":    10,
        "User.orders":    5,
    },
    OnReject: func(ctx context.Context, stats complexity.Stats) {
        slog.Warn("rejected", "complexity", stats.Complexity)
    },
})
```

Rejects queries exceeding limits with structured GraphQL errors containing `QUERY_TOO_COMPLEX` or `QUERY_TOO_DEEP` error codes.

### `contrib/graphql/apq`

Automatic Persisted Queries (Apollo-compatible) and query whitelist:

```go
// APQ mode: cache queries by SHA-256 hash
cache := apq.NewMemoryCache(10000) // or implement apq.Cache for Redis
srv.Use(apq.AutoPersistedQueries(cache))

// Whitelist mode: only pre-registered queries allowed
srv.Use(apq.Whitelist(map[string]string{
    "sha256-of-query": "query { users { id name } }",
}))
```

The `Cache` interface is minimal (Get/Set), easily backed by Redis or any KV store.

### `contrib/graphql/auth`

Authentication extraction from HTTP requests and propagation to downstream services:

```go
// HTTP middleware: extract Bearer token, verify, inject into context
mux.Handle("/query", auth.HTTPMiddleware(
    auth.BearerExtractor(),
    func(ctx context.Context, token string) (auth.UserInfo, error) {
        // Verify token (call auth service, decode JWT, etc.)
        return auth.UserInfo{ID: "u1", Username: "alice"}, nil
    },
)(graphqlHandler))

// In resolver: get user info
user, ok := auth.GetUser(ctx)

// Propagate to downstream gRPC/HTTP calls
meta := auth.OutgoingMetadata(ctx)
// meta = {"authorization": "Bearer ...", "x-user-id": "u1", ...}
```

### `contrib/graphql/subscription`

Dual transport for GraphQL subscriptions:

```go
// WebSocket (graphql-ws protocol)
gql.WithTransport(subscription.WSTransport(
    subscription.WithKeepAlive(30*time.Second),
    subscription.WithPingInterval(20*time.Second),
))

// SSE (Server-Sent Events, no WS upgrade needed)
gql.WithTransport(subscription.SSETransport())
```

### `contrib/graphql/federation`

Federation gateway for multi-subgraph composition:

```go
gw := federation.NewGateway([]federation.SubgraphConfig{
    {Name: "users", URL: "http://user-svc:8081/query"},
    {Name: "orders", URL: "http://order-svc:8082/query"},
}, federation.WithTimeout(10*time.Second))

// Query a specific subgraph
resp, err := gw.QuerySubgraph(ctx, "users", federation.GraphQLRequest{
    Query: `query { user(id: "1") { name } }`,
})

// Fan out to all subgraphs
results := gw.QueryAll(ctx, req)
```

## BFF Pattern: Aggregating Microservices

A typical BFF resolver aggregates multiple backend services:

```go
func (r *queryResolver) User(ctx context.Context, id string) (*User, error) {
    // Auth propagation
    meta := auth.OutgoingMetadata(ctx)

    // Call user service via gRPC (with beauty service discovery)
    conn, _ := grpcclient.DialContext(ctx, "beauty://user-service",
        grpcclient.WithMetadata(meta),
    )
    userResp, err := userpb.NewUserServiceClient(conn).GetUser(ctx, &userpb.GetUserRequest{Id: id})
    if err != nil {
        return nil, err
    }

    return &User{
        ID:    userResp.Id,
        Name:  userResp.Name,
        Email: userResp.Email,
    }, nil
}
```

## Example

See [`examples/graphql-bff`](../examples/graphql-bff) for a complete schema-first BFF:

```bash
cd examples/graphql-bff && go run .
# Playground: http://localhost:8080/
# Endpoint:   http://localhost:8080/query
```

Regenerate after editing `schema.graphql`:

```bash
go install github.com/99designs/gqlgen@v0.17.73
gqlgen generate
```


| Package | Purpose |
|---|---|
| `github.com/99designs/gqlgen` | Schema-first GraphQL engine |
| `github.com/vektah/gqlparser/v2` | GraphQL schema parser (gqlgen transitive) |
| `github.com/rushteam/beauty` | Core framework interfaces |

## What This Module Does NOT Do

- **No ORM/DB coupling** — resolvers call whatever backends they need
- **No schema registry** — subgraphs manage their own schemas
- **No core modification** — pure contrib module, core `go.mod` untouched
- **No code generation** — gqlgen codegen is run by the user, not this module
