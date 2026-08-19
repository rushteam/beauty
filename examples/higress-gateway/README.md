# Higress 网关 + Beauty 微服务 示例

本示例演示如何将 Higress 云原生网关作为 Beauty 微服务的统一入口，实现自动服务发现和路由。

## 架构

```
Client ──▶ Higress (80) ──▶ user-svc (gRPC :9001)
                          ──▶ web-svc  (HTTP :8080)
                  ▲
                  │ McpBridge subscribe
                  ▼
             Nacos (8848)
```

- **user-svc** — gRPC 服务，注册到 Nacos，metadata 自动包含 `protocol=GRPC`
- **web-svc** — HTTP 服务，注册到 Nacos，metadata 自动包含 `protocol=HTTP`
- **Higress** — 读取 Nacos 中的服务列表和 protocol 元数据，自动选择正确的后端协议

## 前置条件

- Docker & Docker Compose
- Go 1.26+（如需本地开发/调试）

## 快速启动

```bash
docker compose up --build
```

等待所有容器就绪后：

### 测试 HTTP 服务

```bash
# 通过 Higress 网关访问 web-svc
curl http://localhost/web/ping
# 预期: {"service":"web-svc","status":"ok"}

curl http://localhost/web/health
# 预期: healthy
```

### 测试 gRPC 服务

```bash
# 通过 Higress 网关访问 user-svc (gRPC health check)
grpcurl -plaintext localhost:80 grpc.health.v1.Health/Check
# 预期: { "status": "SERVING" }
```

## 配置说明

### Higress 路由配置

路由配置位于 `higress/ingress.yaml`，包含：

1. **McpBridge** — 告诉 Higress 从哪个 Nacos 实例订阅服务
2. **Ingress (user-svc)** — 将 `/api.user.v1.UserService` 路由到 gRPC 后端
3. **Ingress (web-svc)** — 将 `/web` 路由到 HTTP 后端

### 服务注册

Beauty 服务无需额外配置。注册到 Nacos 时框架自动注入：

```json
{
  "kind": "grpc",
  "protocol": "GRPC"
}
```

Higress 读取 `protocol` 字段决定后端转发协议。

## 本地开发（不用 Docker）

如果你想本地调试服务，先确保 Nacos 在 `127.0.0.1:8848` 运行，然后：

```bash
# 终端 1: 启动 user-svc
cd user-svc && go run .

# 终端 2: 启动 web-svc
cd web-svc && go run .
```

## 清理

```bash
docker compose down -v
```

## 相关文档

- [Higress 网关架构文档](../../docs/higress-gateway.md)
- [gRPC 服务发现与注册](../../docs/grpc-service-discovery.md)
