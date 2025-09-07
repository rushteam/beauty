# gRPC 客户端服务发现

## 概述

本功能实现了基于服务发现的gRPC客户端，支持自动服务发现、负载均衡、故障转移和地域过滤等功能。客户端可以自动发现服务实例，并根据配置的策略进行连接和调用。

## 功能特点

- **自动服务发现**：自动从注册中心发现服务实例
- **负载均衡**：支持多种负载均衡策略（轮询、随机、权重等）
- **故障转移**：支持自动故障转移和重试机制
- **地域过滤**：支持基于地域、可用区、环境的服务过滤
- **连接管理**：自动管理连接池，支持健康检查
- **服务监听**：实时监听服务变化，自动更新连接

## 使用方法

### 基本用法

```go
package main

import (
    "context"
    "log"
    
    "github.com/rushteam/beauty/pkg/client/grpcclient"
    "github.com/rushteam/beauty/pkg/service/discover/etcdv3"
)

func main() {
    // 创建服务发现客户端
    discovery := etcdv3.NewRegistry(&etcdv3.Config{
        Endpoints: []string{"127.0.0.1:2379"},
        Prefix:    "/beauty",
    })
    
    // 创建客户端工厂
    factory := grpcclient.NewClientFactory(discovery)
    
    // 获取特定服务的客户端
    greeterClient := factory.GetClient("v1alpha.Greeter")
    
    // 获取连接并调用服务
    conn, err := greeterClient.GetClient(context.Background())
    if err != nil {
        log.Fatal(err)
    }
    
    // 使用连接调用服务方法
    // greeterClient := v1.NewGreeterClient(conn)
    // resp, err := greeterClient.SayHello(ctx, &v1.HelloRequest{Name: "World"})
}
```

### 高级用法 - 客户端管理器

```go
// 创建客户端管理器，支持负载均衡和故障转移
manager := grpcclient.NewClientManager(discovery, "v1alpha.Greeter",
    grpcclient.WithLoadBalanceStrategy(grpcclient.RoundRobin),
    grpcclient.WithManagerRegionFilter(
        []string{"us-west-1"}, // 单个地域
        []string{"us-west-1a"}, // 单个可用区
        []string{"campus-1"}, // 单个园区
        []string{"production"}, // 单个环境
    ),
    grpcclient.WithHealthCheck(true, time.Second*30),
    grpcclient.WithFailover(true, 3, time.Second),
)

// 多选过滤示例
multiManager := grpcclient.NewClientManager(discovery, "v1alpha.Greeter",
    grpcclient.WithLoadBalanceStrategy(grpcclient.WeightedRoundRobin),
    grpcclient.WithManagerRegionFilter(
        []string{"us-west-1", "us-west-2"}, // 支持多个地域
        []string{"us-west-1a", "us-west-2a"}, // 支持多个可用区
        []string{"campus-1", "campus-2"}, // 支持多个园区
        []string{"production"}, // 只支持生产环境
    ),
    grpcclient.WithHealthCheck(true, time.Second*30),
    grpcclient.WithFailover(true, 3, time.Second),
)

// 启动管理器
ctx := context.Background()
if err := manager.Start(ctx); err != nil {
    log.Fatal(err)
}

// 调用服务方法（自动负载均衡和故障转移）
err := manager.Call(ctx, "/v1alpha.Greeter/SayHello", req, resp)
if err != nil {
    log.Printf("call failed: %v", err)
}
```

### 地域过滤

```go
// 单个地域过滤
usWestClient := factory.GetClient("v1alpha.Greeter",
    grpcclient.WithDiscoveryRegionFilter(
        []string{"us-west-1"},
        []string{"us-west-1a"},
        []string{"campus-1"},
        []string{"production"},
    ),
)

// 多个地域、可用区、园区、环境的过滤
multiRegionClient := factory.GetClient("v1alpha.Greeter",
    grpcclient.WithDiscoveryRegionFilter(
        []string{"us-west-1", "us-east-1"}, // 支持多个地域
        []string{"us-west-1a", "us-east-1a"}, // 支持多个可用区
        []string{"campus-1", "campus-2"}, // 支持多个园区
        []string{"production", "staging"}, // 支持多个环境
    ),
)

// 部分过滤（只过滤地域，其他不限制）
regionOnlyClient := factory.GetClient("v1alpha.Greeter",
    grpcclient.WithDiscoveryRegionFilter(
        []string{"us-west-1", "us-west-2"}, // 只限制地域
        []string{}, // 不限制可用区
        []string{}, // 不限制园区
        []string{}, // 不限制环境
    ),
)
```

#### 获取服务信息
```go
services, err := multiRegionClient.GetServiceInfo(ctx)
if err != nil {
    log.Printf("failed to get services: %v", err)
} else {
    for _, service := range services {
        log.Printf("Service: %s, Region: %s, Zone: %s, Campus: %s, Environment: %s", 
            service.Addr, 
            service.Metadata["region"], 
            service.Metadata["zone"],
            service.Metadata["campus"],
            service.Metadata["environment"])
    }
}
```

## 负载均衡策略

### 1. 轮询 (RoundRobin)
```go
manager := grpcclient.NewClientManager(discovery, "service-name",
    grpcclient.WithLoadBalanceStrategy(grpcclient.RoundRobin),
)
```

### 2. 随机 (Random)
```go
manager := grpcclient.NewClientManager(discovery, "service-name",
    grpcclient.WithLoadBalanceStrategy(grpcclient.Random),
)
```

### 3. 权重轮询 (WeightedRoundRobin)
```go
manager := grpcclient.NewClientManager(discovery, "service-name",
    grpcclient.WithLoadBalanceStrategy(grpcclient.WeightedRoundRobin),
)
```

### 4. 最少连接 (LeastConnections)
```go
manager := grpcclient.NewClientManager(discovery, "service-name",
    grpcclient.WithLoadBalanceStrategy(grpcclient.LeastConnections),
)
```

## 故障转移配置

```go
manager := grpcclient.NewClientManager(discovery, "service-name",
    grpcclient.WithFailover(true, 3, time.Second),
)
```

参数说明：
- `enabled`: 是否启用故障转移
- `maxRetries`: 最大重试次数
- `retryDelay`: 重试间隔

## 健康检查配置

```go
manager := grpcclient.NewClientManager(discovery, "service-name",
    grpcclient.WithHealthCheck(true, time.Second*30),
)
```

参数说明：
- `enabled`: 是否启用健康检查
- `interval`: 检查间隔

## 连接选项配置

```go
manager := grpcclient.NewClientManager(discovery, "service-name",
    grpcclient.WithManagerDialOptions(
        grpc.WithTimeout(time.Second*5),
        grpc.WithKeepaliveParams(keepalive.ClientParameters{
            Time:                time.Second * 20,
            Timeout:             time.Second * 10,
            PermitWithoutStream: true,
        }),
    ),
)
```

## 拦截器配置

```go
// 一元拦截器
manager := grpcclient.NewClientManager(discovery, "service-name",
    grpcclient.WithUnaryInterceptors(
        loggingInterceptor,
        tracingInterceptor,
    ),
)

// 流拦截器
manager := grpcclient.NewClientManager(discovery, "service-name",
    grpcclient.WithStreamInterceptors(
        streamLoggingInterceptor,
    ),
)
```

## 完整示例

```go
package main

import (
    "context"
    "log"
    "time"
    
    "github.com/rushteam/beauty/pkg/client/grpcclient"
    "github.com/rushteam/beauty/pkg/service/discover/etcdv3"
    "google.golang.org/grpc"
    "google.golang.org/grpc/keepalive"
)

func main() {
    // 创建服务发现
    discovery := etcdv3.NewRegistry(&etcdv3.Config{
        Endpoints: []string{"127.0.0.1:2379"},
        Prefix:    "/beauty",
    })
    
    // 创建客户端管理器
    manager := grpcclient.NewClientManager(discovery, "v1alpha.Greeter",
        grpcclient.WithLoadBalanceStrategy(grpcclient.WeightedRoundRobin),
        grpcclient.WithManagerRegionFilter("us-west-1", "us-west-1a", "production"),
        grpcclient.WithHealthCheck(true, time.Second*30),
        grpcclient.WithFailover(true, 3, time.Second),
        grpcclient.WithManagerDialOptions(
            grpc.WithKeepaliveParams(keepalive.ClientParameters{
                Time:                time.Second * 20,
                Timeout:             time.Second * 10,
                PermitWithoutStream: true,
            }),
        ),
    )
    
    // 启动管理器
    ctx := context.Background()
    if err := manager.Start(ctx); err != nil {
        log.Fatal(err)
    }
    defer manager.Close()
    
    // 调用服务
    req := &HelloRequest{Name: "World"}
    resp := &HelloReply{}
    
    err := manager.Call(ctx, "/v1alpha.Greeter/SayHello", req, resp)
    if err != nil {
        log.Printf("call failed: %v", err)
    } else {
        log.Printf("response: %s", resp.Message)
    }
}
```

## 注意事项

1. **连接管理**：客户端会自动管理连接池，无需手动关闭连接
2. **服务监听**：客户端会实时监听服务变化，自动更新连接
3. **故障转移**：启用故障转移后，调用失败会自动重试
4. **地域过滤**：确保服务端注册时包含正确的地域信息
5. **资源清理**：使用完毕后记得调用 `Close()` 方法清理资源

## 与服务端的配合

客户端的地域过滤需要与服务端注册的地域信息匹配：

```go
// 服务端注册时设置地域信息
grpcServer := grpcserver.New(
    ":58080",
    handler,
    grpcserver.WithRegionInfo("us-west-1", "us-west-1a", "campus-1"),
    grpcserver.WithEnvironment("production"),
    grpcserver.WithAutoServiceDiscovery(registry),
)

// 客户端过滤相同地域的服务
client := factory.GetClient("v1alpha.Greeter",
    grpcclient.WithDiscoveryRegionFilter("us-west-1", "us-west-1a", "production"),
)
```

这样客户端就能自动发现并连接到同地域的服务实例，实现就近访问和负载均衡。
