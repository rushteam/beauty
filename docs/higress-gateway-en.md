# Higress as the Unified Ingress Gateway for Beauty

> 中文版: [higress-gateway.md](higress-gateway.md)

This document describes how to use [Higress](https://higress.io) (a cloud-native API gateway built on Envoy) as the
unified traffic entry point for a beauty microservice cluster, providing automatic service discovery, protocol detection, and routing without configuring each service by hand.

---

## Layered Responsibilities

| Layer | Owned by | Typical capabilities |
|---|---|---|
| External traffic entry | **Higress** | TLS termination, domain routing, CORS, global rate limiting/WAF, gRPC-JSON transcoding, AI gateway |
| Service-level governance | **Beauty middleware** | Business authentication/authorization (auth/authz), service-level circuit breaking/rate limiting, timeouts, accesslog, requestid |
| Business logic | **Beauty Handler** | Your code |

Together the two layers form a **dual line of defense**: the gateway blocks malicious traffic and enforces global policies, while the application layer handles business-level security and resilience.

---

## Prerequisites

- Higress 2.x+ (standalone mode is recommended for a quick trial, or deploy on K8s)
- Nacos 2.x+ (as the service registry; Higress subscribes via gRPC)
- Beauty services registered with Nacos or Consul

---

## How It Works

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

1. When a Beauty service starts, it registers with Nacos and automatically injects `protocol=GRPC/HTTP` into the instance metadata
2. Higress subscribes to the Nacos service list via McpBridge
3. Higress reads the instance's `metadata["protocol"]` to determine the backend protocol
4. When a request reaches Higress, it is routed to the target service according to the Ingress rules

---

## Quick Start (Nacos)

### 1. Register the Beauty Service with Nacos

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
            // register your gRPC services
        },
            beauty.WithServiceName("user-svc"),
        ),
    )
    app.Start(context.Background())
}
```

After registration, the instance metadata in Nacos automatically contains:
```json
{
  "kind": "grpc",
  "protocol": "GRPC"
}
```

### 2. Configure the Higress McpBridge (Connect to Nacos)

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

### 3. Create an Ingress Route

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

Done. Higress automatically discovers `user-svc` from Nacos, reads `protocol=GRPC`, and forwards requests using the gRPC protocol.

---

## Exposing gRPC Services

### Exposing gRPC Directly

If clients send gRPC requests directly (e.g. gRPC-Web or service-to-service calls), Higress automatically selects gRPC forwarding based on
`metadata["protocol"]=GRPC`, with no extra annotations needed.

### gRPC-JSON Transcoding (HTTP Clients Calling gRPC Services)

When HTTP clients need to call a gRPC backend, use Higress's gRPC-JSON transcoding capability:

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

The client sends an HTTP JSON request → Higress automatically converts it to gRPC → the beauty gRPC service handles it.

---

## Exposing HTTP Services

HTTP services are even simpler — beauty registers them with `protocol=HTTP`, and Higress forwards over HTTP by default:

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

## Version Routing / Canary Release

Inject a version label through the beauty service's metadata:

```go
beauty.WithGrpcServer(":9001", registerFn,
    beauty.WithServiceName("user-svc"),
    beauty.WithServiceMetadata(map[string]string{
        "version": "v2",
    }),
)
```

The instance metadata in Nacos becomes `{"protocol":"GRPC","version":"v2"}`.

Combined with the label-routing plugin in Higress, you can direct traffic to instances of the matching version
based on a version identifier in a header / cookie, achieving a canary release. For configuration details see the
[Higress label routing docs](https://higress.io/docs/latest/user/annotation-use-case/#configure-canary-release).

---

## Relationship with the beauty xDS Client

beauty provides the following modes for inter-service calls:

| Mode | Use case | How to use |
|---|---|---|
| **beauty internal discovery** | Service-to-service within the cluster | `grpcclient.Dial("nacos://host/svc")` |
| **Forwarded via Higress** | External traffic entry / cross-cluster | Client → Higress → beauty service |
| **xDS mode** | Inside an Istio mesh | `grpcclient.Dial("xds:///svc")` |

The three do not conflict:
- Internal service-to-service calls use beauty's built-in discovery (low latency, direct connection)
- External entry goes through Higress (unified authentication, rate limiting, TLS)
- If Istio is also deployed, xDS mode lets beauty services be managed by the mesh as well

---

## Consul

Usage with Consul is similar; change the McpBridge configuration to:

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

Ingress destination format: `service-name.service-source-name.consul`

beauty services likewise automatically inject the `protocol` metadata when registering with Consul.

---

## FAQ

### Q: Higress returns 502 and the gRPC service does not respond

Check whether the instance metadata in Nacos contains `protocol=GRPC`. If it doesn't, your beauty version is too old
(this feature was added in v0.7.4+). Upgrade beauty or set it manually:

```go
beauty.WithServiceMetadata(map[string]string{"protocol": "GRPC"})
```

### Q: Can Higress automatically generate a route for every Nacos service?

Yes. Higress supports a "service-source routing" feature (dynamic matching based on the serviceId in the path);
see [Higress dynamic routing](https://github.com/alibaba/higress/issues/459).

### Q: Will Higress drop requests during a beauty service's graceful shutdown?

No. beauty's shutdown order is: deregister from Nacos first → wait for drainDelay (default 5s, giving Higress time
to notice the instance going offline) → stop the service. Combined with Higress's health checks (subscribed to Nacos change notifications),
this achieves zero packet loss during rolling releases.

### Q: How does this order relative to the beauty middleware chain?

The full path a request travels through:

```
Client → Higress(WAF/rate limiting/TLS/CORS) → Beauty(accesslog→requestid→recovery→auth→handler)
```

It is recommended to put global policies (IP allow/deny lists, HTTP-flood (CC) protection) in Higress wasm plugins, and business-level policies (JWT authentication, RBAC)
in beauty middleware.

---

## Further Reading

- [Higress AI gateway integration](higress-ai-gateway-en.md) — LLM request proxying + MCP endpoint exposure
- [examples/higress-ai](../examples/higress-ai/) — runnable AI integration example
