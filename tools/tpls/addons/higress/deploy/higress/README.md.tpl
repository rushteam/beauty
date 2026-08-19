# Higress 网关部署说明

本目录包含 [Higress](https://higress.io) 云原生网关与 `{{.Name}}` 服务集成所需的配置。

## 文件说明

- **mcpbridge.yaml** — McpBridge 配置，告诉 Higress 从 Nacos 订阅服务
- **ingress.yaml** — Ingress 路由规则，将流量导向 `{{.Name}}` 服务

## 前置条件

1. Higress 已部署（standalone 模式或 K8s Operator）
2. Nacos 2.x 运行中（默认 127.0.0.1:8848）
3. `{{.Name}}` 服务已注册到 Nacos

## 使用步骤

### Standalone 模式

将配置文件放到 Higress 数据目录：

```bash
cp mcpbridge.yaml /path/to/higress/data/
cp ingress.yaml /path/to/higress/data/
```

### Kubernetes 模式

```bash
kubectl apply -f mcpbridge.yaml
kubectl apply -f ingress.yaml
```

## 验证

```bash
# HTTP 服务
curl http://localhost/

# gRPC 服务
grpcurl -plaintext localhost:80 grpc.health.v1.Health/Check
```

## 自定义

- 修改 `mcpbridge.yaml` 中的 Nacos 地址和命名空间
- 修改 `ingress.yaml` 中的路径规则和域名
- 如需灰度发布，在服务注册时添加 version metadata 并配合 Higress 标签路由插件

## 参考文档

- [Beauty Higress 网关架构文档](https://github.com/rushteam/beauty/blob/main/docs/higress-gateway.md)
- [Higress 官方文档](https://higress.io/docs)
