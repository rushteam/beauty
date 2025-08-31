# Kubernetes 服务发现

这个包提供了基于 Kubernetes 的服务发现功能，支持自动发现集群内的服务实例。

## 功能特性

- **自动服务发现**: 自动发现 Kubernetes Service 和 Endpoints
- **服务变更监听**: 实时监听服务实例的变化
- **多端口支持**: 支持多端口服务的端口过滤
- **标签选择器**: 支持通过标签选择器过滤服务
- **命名空间隔离**: 支持指定命名空间进行服务发现
- **集群内外支持**: 支持集群内运行和通过 kubeconfig 外部访问

## 配置参数

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `kubeconfig` | string | "" | kubeconfig 文件路径，为空时使用集群内配置 |
| `namespace` | string | "default" | 目标命名空间 |
| `service_type` | string | "ClusterIP" | 服务类型过滤 |
| `port_name` | string | "" | 端口名称过滤，用于多端口服务 |
| `label_selector` | string | "" | 标签选择器，例如 "app=my-service,version=v1" |
| `watch_timeout` | int | 30 | 监听超时时间（秒） |

## 使用方法

### 1. 基本使用

```go
import (
    "context"
    "github.com/rushteam/beauty/pkg/service/discover/k8s"
)

// 创建配置
config := &k8s.Config{
    Namespace:     "default",
    ServiceType:   "ClusterIP",
    LabelSelector: "app=my-service",
}

// 创建注册中心
registry := k8s.NewRegistry(config)

// 查找服务实例
ctx := context.Background()
services, err := registry.Find(ctx, "my-service")
```

### 2. gRPC 集成

```go
import (
    _ "github.com/rushteam/beauty/pkg/service/discover/k8s"
    "google.golang.org/grpc"
)

// 直接在 gRPC 客户端中使用
conn, err := grpc.Dial("k8s://default/my-service", grpc.WithInsecure())
```

### 3. URL 配置格式

```
k8s://[namespace][/service_type]?[query_params]
```

示例：
- `k8s://default` - 使用默认命名空间和集群内配置
- `k8s://my-namespace?kubeconfig=/path/to/config` - 指定命名空间和配置文件
- `k8s://default?label_selector=app=my-service,version=v1` - 使用标签选择器
- `k8s://default/ClusterIP?port_name=http` - 指定服务类型和端口名

### 4. 服务监听

```go
// 监听服务变化
err := registry.Watch(ctx, "my-service", func(services []discover.ServiceInfo) {
    log.Printf("服务变化，当前实例数: %d", len(services))
    for _, svc := range services {
        log.Printf("  - %s: %s", svc.Name, svc.Addr)
    }
})
```

## 部署要求

### 集群内运行

当应用在 Kubernetes 集群内运行时，需要确保 Pod 有足够的权限访问 Kubernetes API：

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: my-app-sa
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: service-discovery-role
rules:
- apiGroups: [""]
  resources: ["services", "endpoints"]
  verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: service-discovery-binding
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: service-discovery-role
subjects:
- kind: ServiceAccount
  name: my-app-sa
  namespace: default
```

### 集群外运行

当应用在集群外运行时，需要提供有效的 kubeconfig 文件：

```go
config := &k8s.Config{
    Kubeconfig: "/path/to/kubeconfig",
    Namespace:  "default",
}
```

## 注意事项

1. **权限要求**: 应用需要有读取 Services 和 Endpoints 资源的权限
2. **网络连通性**: 确保应用能够访问发现的服务实例地址
3. **监听重连**: 当网络中断时，监听会自动重连
4. **资源清理**: 使用完毕后调用 `registry.Close()` 清理资源

## 示例

完整的使用示例请参考 `examples/services/k8s/main.go` 文件。
