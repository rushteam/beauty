# gRPC 服务自动发现与注册

## 概述

本功能实现了基于protobuf定义的gRPC服务的自动发现和注册。当启用此功能时，框架会自动从gRPC Server中读取已注册的protobuf服务，并将每个服务作为独立的服务实例注册到服务发现中心。

## 功能特点

- **自动发现**：无需手动指定ServiceDesc，自动从gRPC Server中读取已注册的服务
- **按服务注册**：每个protobuf服务作为独立的服务注册到注册中心
- **完整元数据**：自动获取服务名称、方法列表、proto文件信息等完整元数据
- **多注册中心支持**：支持同时注册到多个注册中心（etcd、nacos、polaris等）
- **零配置**：只需要在handler中注册服务，框架自动处理服务发现

## 使用方法

### 地域信息配置

为了兼容Polaris等注册中心，框架提供了地域信息配置选项：

```go
grpcServer := grpcserver.New(
    ":58080",
    func(s *grpc.Server) {
        v1.RegisterGreeterServer(s, &GreeterServer{})
    },
    grpcserver.WithServiceName("my-grpc-server"),
    // 设置地域信息，兼容Polaris
    grpcserver.WithRegionInfo("us-west-1", "us-west-1a", "campus-1"),
    grpcserver.WithEnvironment("production"),
    grpcserver.WithWeight(100),
    grpcserver.WithPriority(0),
    grpcserver.WithAutoServiceDiscovery(registries...),
)
```

#### 地域信息选项说明

- `WithRegionInfo(region, zone, campus)`: 设置地域、可用区、园区信息
- `WithEnvironment(env)`: 设置环境信息（如：production、staging、development）
- `WithWeight(weight)`: 设置服务权重（用于负载均衡）
- `WithPriority(priority)`: 设置服务优先级（用于故障转移）

### 基本用法

```go
package main

import (
    "context"
    "log"
    
    "github.com/rushteam/beauty"
    "github.com/rushteam/beauty/pkg/service/discover/etcdv3"
    "github.com/rushteam/beauty/pkg/service/grpcserver"
    grpcpkg "google.golang.org/grpc"
)

func main() {
    // 创建gRPC服务器，启用自动服务发现
    grpcServer := grpcserver.New(
        ":58080",
        func(s *grpcpkg.Server) {
            // 注册protobuf服务
            v1.RegisterGreeterServer(s, &GreeterServer{})
            v1.RegisterUserServiceServer(s, &UserServiceServer{})
        },
        grpcserver.WithServiceName("my-grpc-server"),
        grpcserver.WithMetadata(map[string]string{
            "version": "v1.0",
        }),
        // 设置地域信息，兼容Polaris
        grpcserver.WithRegionInfo("us-west-1", "us-west-1a", "campus-1"),
        grpcserver.WithEnvironment("production"),
        grpcserver.WithWeight(100),
        grpcserver.WithPriority(0),
        // 启用自动服务发现
        grpcserver.WithAutoServiceDiscovery(
            etcdv3.NewRegistry(&etcdv3.Config{
                Endpoints: []string{"127.0.0.1:2379"},
                Prefix:    "/beauty",
                TTL:       10,
            }),
        ),
    )
    
    // 创建应用
    app := beauty.New(
        beauty.WithService(grpcServer),
    )
    
    app.Start(context.Background())
}
```

### 多注册中心支持

```go
grpcServer := grpcserver.New(
    ":58080",
    func(s *grpcpkg.Server) {
        v1.RegisterGreeterServer(s, &GreeterServer{})
        v1.RegisterUserServiceServer(s, &UserServiceServer{})
    },
    grpcserver.WithServiceName("my-grpc-server"),
    grpcserver.WithAutoServiceDiscovery(
        // 注册到etcd
        etcdv3.NewRegistry(&etcdv3.Config{
            Endpoints: []string{"127.0.0.1:2379"},
            Prefix:    "/beauty",
        }),
        // 注册到nacos
        nacos.NewRegistry(&nacos.Config{
            Addr:      []string{"127.0.0.1:8848"},
            Namespace: "default",
            Group:     "DEFAULT_GROUP",
        }),
        // 注册到polaris
        polaris.NewRegistry(&polaris.Config{
            Addresses: []string{"127.0.0.1:8091"},
            Namespace: "default",
        }),
    ),
)
```

## 注册中心中的服务信息

启用自动服务发现后，在注册中心中会看到以下服务：

### 服务列表
- `v1alpha.Greeter` - Greeter服务（包含SayHello方法）
- `v1alpha.UserService` - UserService服务（包含CreateUser、GetUser等方法）

### 服务元数据
每个服务包含以下元数据：
- `kind`: "grpc"
- `methods`: 方法列表，如 `["SayHello"]`
- `proto_file`: proto文件信息，如 `"greeter.proto"`
- `region`: 地域信息，如 `"us-west-1"`
- `zone`: 可用区信息，如 `"us-west-1a"`
- `campus`: 园区信息，如 `"campus-1"`
- `environment`: 环境信息，如 `"production"`
- `weight`: 服务权重，如 `"100"`
- `priority`: 服务优先级，如 `"0"`
- 用户自定义元数据（通过WithMetadata设置）

## 实现原理

1. **服务发现**：使用gRPC内置的 `GetServiceInfo()` 方法获取已注册的服务信息
2. **信息解析**：解析服务名称、方法列表、proto文件信息等元数据
3. **服务包装**：为每个protobuf服务创建独立的 `ProtoServiceWrapper` 实例
4. **批量注册**：将每个服务分别注册到所有配置的注册中心

## 与传统方式的对比

### 传统方式
```go
// 整个gRPC服务器作为一个服务注册
app := beauty.New(
    beauty.WithService(grpcServer),
    beauty.WithRegistry(etcdRegistry), // 注册整个服务器
)
```

### 自动服务发现方式
```go
// 每个protobuf服务作为独立服务注册
app := beauty.New(
    beauty.WithService(grpcServer),
    // 不需要全局注册，自动服务发现已处理
)
```

## 注意事项

1. **服务名称**：使用protobuf定义的服务名称作为注册的服务名
2. **元数据合并**：服务器级别的元数据会与每个服务的元数据合并
3. **错误处理**：如果服务发现失败，会记录错误但不影响服务器启动
4. **性能影响**：服务发现是异步进行的，不会影响gRPC服务器的启动性能

## 示例项目

完整的使用示例请参考 `examples/grpc-service-discovery/main.go`。
