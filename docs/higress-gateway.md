# Higress 作为 Beauty 统一入口网关

本文档介绍如何将 [Higress](https://higress.io)（基于 Envoy 的云原生 API 网关）作为
beauty 微服务集群的统一流量入口，实现自动服务发现、协议识别与路由，无需手动逐服务配置。

---

## 分层职责

| 层 | 由谁负责 | 典型能力 |
|---|---|---|
| 外部流量入口 | **Higress** | TLS 终止、域名路由、CORS、全局限流/WAF、gRPC-JSON transcoding、AI 网关 |
| 服务级治理 | **Beauty 中间件** | 业务鉴权(auth/authz)、服务级熔断/限流、超时、accesslog、requestid |
| 业务逻辑 | **Beauty Handler** | 你的代码 |

两层配合形成**双重防线**：网关拦截恶意流量和全局策略，应用层处理业务级安全与韧性。

---

## 前置条件

- Higress 2.x+（推荐 standalone 模式快速体验，或 K8s 部署）
- Nacos 2.x+（作为服务注册中心，Higress 通过 gRPC 订阅）
- Beauty 服务使用 Nacos 或 Consul 注册

---

## 工作原理

```
┌─────────┐       ┌──────────────┐       ┌─────────┐
│  Client  │──────▶│   Higress    │──────▶│ Beauty  │
│          │       │  (Gateway)   │       │ Service │
└─────────┘       └──────┬───────┘       └────┬────┘
                          │                     │
                          │ McpBridge            │ Register
                          ▼                     ▼
                   ┌─────────────┐       ┌─────────────┐
                   │   Nacos     │◀──────│   Nacos     │
                   │ (subscribe) │       │ (publish)   │
                   └─────────────┘       └─────────────┘
```

1. Beauty 服务启动时注册到 Nacos，自动在实例元数据中注入 `protocol=GRPC/HTTP`
2. Higress 通过 McpBridge 订阅 Nacos 服务列表
3. Higress 读取实例 `metadata["protocol"]` 确定后端协议
4. 请求到达 Higress 时按 Ingress 规则路由到目标服务

---

## 快速开始（Nacos 场景）

### 1. Beauty 服务注册到 Nacos

```go
package main

import (
    "context"
    "github.com/rushteam/beauty"
    "github.com/rushteam/beauty/pkg/service/discover/nacos"
    "github.com/rushteam/beauty/pkg/service/grpcserver"
    "google.golang.org/grpc"
)

func main() {
    registry := nacos.NewRegistry(&nacos.Config{
        Addr:      []string{"127.0.0.1:8848"},
        Namespace: "public",
        Group:     "DEFAULT_GROUP",
    })

    app := beauty.New(
        beauty.WithRegistry(registry),
        beauty.WithGrpcServer(":9001", func(s *grpc.Server) {
            // 注册你的 gRPC 服务
        },
            beauty.WithServiceName("user-svc"),
        ),
    )
    app.Start(context.Background())
}
```

服务注册后，Nacos 中实例元数据自动包含：
```json
{
  "kind": "grpc",
  "protocol": "GRPC"
}
```

### 2. 配置 Higress McpBridge（连接 Nacos）

```yaml
apiVersion: networking.higress.io/v1
kind: McpBridge
metadata:
  name: default
  namespace: higress-system
spec:
  registries:
  - name: my-nacos
    type: nacos2
    domain: 127.0.0.1
    port: 8848
    nacosNamespaceId: public
    nacosGroups:
    - DEFAULT_GROUP
```

### 3. 创建 Ingress 路由

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  annotations:
    higress.io/destination: user-svc.DEFAULT-GROUP.public.nacos
  name: user-svc-route
spec:
  ingressClassName: higress
  rules:
  - host: api.example.com
    http:
      paths:
      - path: /user
        pathType: Prefix
        backend:
          resource:
            apiGroup: networking.higress.io
            kind: McpBridge
            name: default
```

完成。Higress 会自动从 Nacos 发现 `user-svc`，读取 `protocol=GRPC`，用 gRPC 协议转发请求。

---

## gRPC 服务暴露

### 直接暴露 gRPC

如果客户端直接发 gRPC 请求（如 gRPC-Web 或服务间调用），Higress 会根据
`metadata["protocol"]=GRPC` 自动选择 gRPC 转发，无需额外注解。

### gRPC-JSON Transcoding（HTTP 客户端调 gRPC 服务）

当 HTTP 客户端需要调用 gRPC 后端时，使用 Higress 的 gRPC-JSON transcoding 能力：

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  annotations:
    higress.io/destination: user-svc.DEFAULT-GROUP.public.nacos
    higress.io/backend-protocol: "GRPC"
  name: user-svc-grpc
spec:
  ingressClassName: higress
  rules:
  - host: api.example.com
    http:
      paths:
      - path: /api.user.v1.UserService
        pathType: Prefix
        backend:
          resource:
            apiGroup: networking.higress.io
            kind: McpBridge
            name: default
```

客户端发 HTTP JSON 请求 → Higress 自动转为 gRPC → beauty gRPC 服务处理。

---

## HTTP 服务暴露

HTTP 服务更简单——beauty 注册时 `protocol=HTTP`，Higress 默认用 HTTP 转发：

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  annotations:
    higress.io/destination: web-svc.DEFAULT-GROUP.public.nacos
  name: web-svc-route
spec:
  ingressClassName: higress
  rules:
  - host: api.example.com
    http:
      paths:
      - path: /web
        pathType: Prefix
        backend:
          resource:
            apiGroup: networking.higress.io
            kind: McpBridge
            name: default
```

---

## 版本路由 / 灰度发布

通过 beauty 服务的 metadata 注入 version 标签：

```go
beauty.WithGrpcServer(":9001", registerFn,
    beauty.WithServiceName("user-svc"),
    beauty.WithServiceMetadata(map[string]string{
        "version": "v2",
    }),
)
```

Nacos 中实例 metadata 为 `{"protocol":"GRPC","version":"v2"}`。

在 Higress 中配合标签路由插件，可以按 header / cookie 中的版本标识将流量导向对应
版本的实例，实现灰度发布。具体配置参见
[Higress 标签路由文档](https://higress.io/docs/latest/user/annotation-use-case/#configure-canary-release)。

---

## 与 beauty xDS 客户端的关系

beauty 提供两种服务间调用模式：

| 模式 | 适用场景 | 使用方式 |
|---|---|---|
| **beauty 内部发现** | 集群内 service-to-service | `grpcclient.Dial("nacos://host/svc")` |
| **经 Higress 转发** | 外部流量入口 / 跨集群 | 客户端 → Higress → beauty 服务 |
| **xDS 模式** | Istio 网格内 | `grpcclient.Dial("xds:///svc")` |

三者不冲突：
- 内部服务间调用走 beauty 自带发现（低延迟，直连）
- 外部入口走 Higress（统一鉴权、限流、TLS）
- 若同时部署 Istio，xDS 模式让 beauty 服务也受 mesh 管理

---

## Consul 场景

Consul 用法类似，McpBridge 配置改为：

```yaml
spec:
  registries:
  - name: my-consul
    type: consul
    domain: 127.0.0.1
    port: 8500
    consulDatacenter: dc1
    consulServiceTag: beauty
```

Ingress destination 格式：`service-name.service-source-name.consul`

beauty 服务注册到 Consul 时同样自动注入 `protocol` 元数据。

---

## 常见问题

### Q: Higress 报 502，gRPC 服务无响应

检查 Nacos 中实例的 metadata 是否包含 `protocol=GRPC`。如果没有，说明 beauty 版本过旧
（此功能在 v0.7.4+ 加入）。升级 beauty 或手动设置：

```go
beauty.WithServiceMetadata(map[string]string{"protocol": "GRPC"})
```

### Q: 能否让 Higress 自动为每个 Nacos 服务生成路由？

可以。Higress 支持"服务来源路由"功能（基于 path 中的 serviceId 动态匹配），
参见 [Higress 动态路由](https://github.com/alibaba/higress/issues/459)。

### Q: beauty 服务优雅停机时 Higress 会丢请求吗？

不会。beauty 停机顺序是：先从 Nacos 注销 → 等 drainDelay（默认 5s，让 Higress
感知实例下线）→ 停止服务。配合 Higress 的健康检查（订阅 Nacos 变更通知），
实现滚动发布零丢包。

### Q: 与 beauty 中间件链的顺序？

请求流经的完整链路：

```
Client → Higress(WAF/限流/TLS/CORS) → Beauty(accesslog→requestid→recovery→auth→handler)
```

建议全局策略（IP黑白名单、CC防护）放 Higress wasm 插件，业务级策略（JWT鉴权、RBAC）
放 beauty 中间件。

---

## 延伸阅读

- [Higress AI 网关集成](higress-ai-gateway.md) — LLM 请求代理 + MCP 端点暴露
- [examples/higress-ai](../examples/higress-ai/) — 可运行的 AI 集成示例
