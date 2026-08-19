# contrib/graphql —— GraphQL/BFF 层(独立模块)

基于 [gqlgen](https://github.com/99designs/gqlgen) schema-first 封装为 `beauty.Service` +
`discover.Service`,并提供 BFF 常用子包:DataLoader、复杂度限制、APQ、认证透传、Federation、Subscription。

```bash
go get github.com/rushteam/beauty/contrib/graphql@latest
```

## 基本用法

```go
import (
    gqlx "github.com/rushteam/beauty/contrib/graphql"
    "github.com/rushteam/beauty/contrib/graphql/apq"
    "github.com/rushteam/beauty/contrib/graphql/auth"
    "github.com/rushteam/beauty/contrib/graphql/dataloader"
)

// es 为 gqlgen 生成的 generated.NewExecutableSchema(...)
srv := gqlx.New(":8080", es,
    gqlx.WithName("bff"),
    gqlx.WithPlayground(true),
    gqlx.WithComplexityLimit(300),
    gqlx.WithExtension(apq.New(apq.NewMemoryCache(1000))),
    gqlx.WithMiddleware(
        dataloader.Middleware(),           // 每请求注入 DataLoader context
        auth.ExtractMiddleware(extractor), // 认证 → gqlgen context
    ),
)

app := beauty.New(
    beauty.WithService(srv),
    beauty.WithRegistry(etcd),
)
```

默认路径:Playground `/`, GraphQL `/query`。实现 `ReadyNotifier`,可注册到服务发现。

## 子包

| 子包 | 能力 |
|---|---|
| `dataloader` | 泛型 DataLoader,HTTP 中间件按请求生命周期注入,解决 N+1 |
| `apq` | Automatic Persisted Queries(兼容 Apollo 客户端) |
| `auth` | 从 HTTP 提取 token/user,透传到 gRPC metadata / HTTP header |
| `complexity` | 查询复杂度限制(gqlgen extension) |
| `federation` | Apollo Federation 网关 |
| `subscription` | WebSocket / SSE Subscription transport |

## 边界

Schema 设计、resolver 业务逻辑、数据源选择都是 policy;本包负责 gqlgen 集成与 BFF 机制。
依赖 beauty core(实现 `beauty.Service`)。Subscription transport 通过 `WithTransport` 按需添加。
