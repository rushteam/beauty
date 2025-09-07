# gRPC DialContext 简化 API

类似 Polaris 风格的简化拨号 API，提供更简洁的服务发现客户端使用方式。

## 概述

`DialContext` API 参考了 `grpc-go-polaris` 的设计理念，提供了一个简洁的函数式 API，让用户可以像使用原生 gRPC 一样简单地使用服务发现功能，同时保持 Beauty 框架的强大特性。

## 设计对比

### Polaris 风格
```go
conn, err := polaris.DialContext(ctx, "polaris://QuickStartEchoServerGRPC",
    polaris.WithGRPCDialOptions(grpc.WithTransportCredentials(insecure.NewCredentials())),
    polaris.WithDisableRouter(),
)
```

### Beauty 风格
```go
conn, err := grpcclient.DialContext(ctx, "beauty://v1alpha.Greeter?env=production",
    grpcclient.WithGRPCDialOptions(grpc.WithTransportCredentials(insecure.NewCredentials())),
    grpcclient.WithDisableRouter(),
)
```

## 支持的 Target 格式

### 1. Beauty 协议（推荐）
```go
// 基础用法
"beauty://serviceName"

// 带环境参数
"beauty://serviceName?env=production"

// 多参数过滤
"beauty://serviceName?env=production&region=us-west-1&tier=frontend"
```

### 2. 直接指定注册中心
```go
// 使用 etcd
"etcd://127.0.0.1:2379/serviceName"

// 使用 nacos
"nacos://127.0.0.1:8848/serviceName?namespace=production&group=DEFAULT_GROUP"
```

## 基础用法

### 最简单的使用方式

```go
import "github.com/rushteam/beauty/pkg/client/grpcclient"

// 最简单的拨号
conn, err := grpcclient.DialContext(ctx, "beauty://v1alpha.UserService")
if err != nil {
    return err
}
defer conn.Close()

// 直接使用
client := pb.NewUserServiceClient(conn)
resp, err := client.GetUser(ctx, &pb.GetUserRequest{Id: "123"})
```

### 带参数的拨号

```go
// 带环境过滤
conn, err := grpcclient.DialContext(ctx, "beauty://v1alpha.UserService?env=production")

// 多参数过滤
conn, err := grpcclient.DialContext(ctx, 
    "beauty://v1alpha.UserService?env=production&region=us-west-1&tier=frontend")
```

### 无 Context 版本

```go
// 使用默认 context.Background()
conn, err := grpcclient.Dial("beauty://v1alpha.UserService?env=production")
```

## 高级用法

### 自定义注册中心

```go
// 使用自定义的 etcd 注册中心
etcdRegistry := etcdv3.NewRegistry(&etcdv3.Config{
    Endpoints: []string{"127.0.0.1:2379"},
    Prefix:    "/beauty",
    TTL:       10,
})

conn, err := grpcclient.DialContext(ctx, "beauty://v1alpha.UserService",
    grpcclient.WithRegistry(etcdRegistry),
    grpcclient.WithTimeout(time.Second*5),
)
```

### 高级标签过滤

```go
// 使用复杂的标签选择器
labelFilter := grpcclient.NewLabelFilter().
    WithMatchLabel("environment", "production").
    WithExpression("tier", selector.FilterOpIn, "frontend", "api").
    WithExpression("deprecated", selector.FilterOpNotExist)

conn, err := grpcclient.DialContext(ctx, "beauty://v1alpha.UserService",
    grpcclient.WithLabelFilter(labelFilter),
    grpcclient.WithLoadBalancer("weighted_round_robin"),
)
```

### 自定义 gRPC 选项

```go
conn, err := grpcclient.DialContext(ctx, "beauty://v1alpha.UserService",
    grpcclient.WithGRPCDialOptions(
        grpc.WithTransportCredentials(insecure.NewCredentials()),
        grpc.WithKeepaliveParams(keepalive.ClientParameters{
            Time:    time.Second * 30,
            Timeout: time.Second * 5,
        }),
    ),
    grpcclient.WithTimeout(time.Second*10),
)
```

## 选项参考

### 核心选项

#### `WithRegistry(discover.Discovery)`
设置自定义的服务注册中心。

```go
grpcclient.WithRegistry(etcdRegistry)
```

#### `WithLabelFilter(*ServiceLabelFilter)`
设置高级标签过滤器。

```go
filter := grpcclient.NewLabelFilter().
    WithMatchLabel("env", "production").
    WithRegionIn("us-west-1", "us-west-2")

grpcclient.WithLabelFilter(filter)
```

#### `WithGRPCDialOptions(...grpc.DialOption)`
设置原生 gRPC 连接选项。

```go
grpcclient.WithGRPCDialOptions(
    grpc.WithTransportCredentials(insecure.NewCredentials()),
)
```

### 便捷选项

#### `WithTimeout(time.Duration)`
设置连接超时时间。

```go
grpcclient.WithTimeout(time.Second * 5)
```

#### `WithEnvironment(string)`
设置环境过滤（便捷方法）。

```go
grpcclient.WithEnvironment("production")
```

#### `WithRegion(string)`
设置地域过滤（便捷方法）。

```go
grpcclient.WithRegion("us-west-1")
```

#### `WithLoadBalancer(string)`
设置负载均衡策略。

```go
grpcclient.WithLoadBalancer("round_robin")     // 轮询
grpcclient.WithLoadBalancer("weighted_random") // 加权随机
grpcclient.WithLoadBalancer("p2c_ewma")        // P2C EWMA
```

### 向后兼容选项

#### `WithRegionFilter(regions, zones, campuses, environments []string)`
使用传统的地域过滤器（向后兼容）。

```go
grpcclient.WithRegionFilter(
    []string{"us-west-1", "us-west-2"}, // regions
    []string{"us-west-1a"},             // zones
    []string{"campus-1"},               // campuses
    []string{"production"},             // environments
)
```

## URL 参数支持

### 环境参数
```go
"beauty://service?env=production"
"beauty://service?environment=production"
```

### 地域参数
```go
"beauty://service?region=us-west-1"
"beauty://service?region=us-west-1,us-west-2"  // 多地域
"beauty://service?zone=us-west-1a"
"beauty://service?campus=campus-1"
```

### 自定义标签
```go
"beauty://service?tier=frontend&version=v1.0&custom-label=value"
```

### 注册中心参数
```go
// nacos 特定参数
"nacos://127.0.0.1:8848/service?namespace=production&group=DEFAULT_GROUP"
```

## 完整示例

### 基础服务调用

```go
package main

import (
    "context"
    "log"
    "time"

    "github.com/rushteam/beauty/pkg/client/grpcclient"
    pb "your-project/api/v1"
)

func main() {
    ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
    defer cancel()

    // 拨号连接
    conn, err := grpcclient.DialContext(ctx, 
        "beauty://v1alpha.UserService?env=production&region=us-west-1")
    if err != nil {
        log.Fatal(err)
    }
    defer conn.Close()

    // 创建客户端
    client := pb.NewUserServiceClient(conn)

    // 调用服务
    resp, err := client.GetUser(ctx, &pb.GetUserRequest{
        Id: "user-123",
    })
    if err != nil {
        log.Fatal(err)
    }

    log.Printf("User: %+v", resp)
}
```

### 高级配置示例

```go
// 使用自定义注册中心和复杂过滤器
etcdRegistry := etcdv3.NewRegistry(&etcdv3.Config{
    Endpoints: []string{"etcd-1:2379", "etcd-2:2379", "etcd-3:2379"},
    Prefix:    "/microservices",
    TTL:       30,
})

labelFilter := grpcclient.NewLabelFilter().
    WithMatchLabels(map[string]string{
        "environment": "production",
        "datacenter":  "us-west",
    }).
    WithExpression("version", selector.FilterOpIn, "v2.0", "v2.1").
    WithExpression("maintenance", selector.FilterOpNotExist)

conn, err := grpcclient.DialContext(ctx, "beauty://v1alpha.OrderService",
    grpcclient.WithRegistry(etcdRegistry),
    grpcclient.WithLabelFilter(labelFilter),
    grpcclient.WithLoadBalancer("weighted_round_robin"),
    grpcclient.WithTimeout(time.Second*5),
    grpcclient.WithGRPCDialOptions(
        grpc.WithTransportCredentials(insecure.NewCredentials()),
        grpc.WithKeepaliveParams(keepalive.ClientParameters{
            Time:                time.Second * 30,
            Timeout:             time.Second * 5,
            PermitWithoutStream: true,
        }),
    ),
)
```

## 与其他 API 的对比

| 特性 | DialContext | Factory | Manager |
|------|-------------|---------|---------|
| **易用性** | ⭐⭐⭐⭐⭐ 最简单 | ⭐⭐⭐ 中等 | ⭐⭐ 复杂 |
| **连接管理** | 单连接 | 连接池 | 高级管理 |
| **负载均衡** | gRPC 内置 | 基础支持 | 高级策略 |
| **健康检查** | gRPC 内置 | 基础支持 | 完整支持 |
| **故障转移** | 需手动实现 | 基础支持 | 完整支持 |
| **适用场景** | 简单调用 | 一般应用 | 复杂治理 |

## 最佳实践

### 1. 选择合适的 API
- **简单服务调用**: 使用 `DialContext`
- **需要连接复用**: 使用 `Factory`
- **复杂治理需求**: 使用 `Manager`

### 2. 错误处理
```go
conn, err := grpcclient.DialContext(ctx, target)
if err != nil {
    // 处理连接错误
    return fmt.Errorf("failed to connect to %s: %w", target, err)
}
defer conn.Close()
```

### 3. 超时控制
```go
// 设置合理的超时时间
ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
defer cancel()

conn, err := grpcclient.DialContext(ctx, target,
    grpcclient.WithTimeout(time.Second*5), // 连接超时
)
```

### 4. 生产环境配置
```go
// 生产环境推荐配置
conn, err := grpcclient.DialContext(ctx, target,
    grpcclient.WithRegistry(productionRegistry),
    grpcclient.WithEnvironment("production"),
    grpcclient.WithLoadBalancer("p2c_ewma"),
    grpcclient.WithTimeout(time.Second*5),
    grpcclient.WithGRPCDialOptions(
        grpc.WithKeepaliveParams(keepalive.ClientParameters{
            Time:                time.Second * 30,
            Timeout:             time.Second * 5,
            PermitWithoutStream: true,
        }),
    ),
)
```

## 迁移指南

### 从 Factory 迁移到 DialContext

**之前**:
```go
factory := grpcclient.NewClientFactory(discovery)
client := factory.GetClient("v1alpha.UserService")
conn, err := client.GetClient(ctx)
```

**现在**:
```go
conn, err := grpcclient.DialContext(ctx, "beauty://v1alpha.UserService")
```

### 从传统 gRPC 迁移

**之前**:
```go
conn, err := grpc.NewClient("localhost:8080", grpc.WithTransportCredentials(insecure.NewCredentials()))
```

**现在**:
```go
conn, err := grpcclient.DialContext(ctx, "beauty://v1alpha.UserService",
    grpcclient.WithGRPCDialOptions(grpc.WithTransportCredentials(insecure.NewCredentials())),
)
```

这样既保持了原生 gRPC 的简洁性，又获得了服务发现的强大功能！
