# gRPC Service Auto-Discovery and Registration

## Overview

This feature implements automatic discovery and registration of gRPC services defined via protobuf. When enabled, the framework automatically reads registered protobuf services from the gRPC Server and registers each service as an independent service instance with the service discovery center.

## Features

- **Automatic discovery**: No need to manually specify ServiceDesc; services are read automatically from the gRPC Server
- **Per-service registration**: Each protobuf service is registered independently with the registry
- **Complete metadata**: Automatically collects service name, method list, proto file info, and other metadata
- **Multi-registry support**: Supports simultaneous registration to multiple registries (etcd, nacos, polaris, etc.)
- **Zero configuration**: Just register services in the handler; the framework handles service discovery automatically

## Usage

### Region Information Configuration

To support registries such as Polaris, the framework provides region information configuration options:

```go
grpcServer := grpcserver.New(
    ":58080",
    func(s *grpc.Server) {
        v1.RegisterGreeterServer(s, &GreeterServer{})
    },
    grpcserver.WithServiceName("my-grpc-server"),
    // Set region info for Polaris compatibility
    grpcserver.WithRegionInfo("us-west-1", "us-west-1a", "campus-1"),
    grpcserver.WithEnvironment("production"),
    grpcserver.WithWeight(100),
    grpcserver.WithPriority(0),
    grpcserver.WithAutoServiceDiscovery(registries...),
)
```

#### Region Information Options

- `WithRegionInfo(region, zone, campus)`: Set region, availability zone, and campus information
- `WithEnvironment(env)`: Set environment (e.g., production, staging, development)
- `WithWeight(weight)`: Set service weight (for load balancing)
- `WithPriority(priority)`: Set service priority (for failover)

### Basic Usage

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
    // Create gRPC server with auto service discovery enabled
    grpcServer := grpcserver.New(
        ":58080",
        func(s *grpcpkg.Server) {
            // Register protobuf services
            v1.RegisterGreeterServer(s, &GreeterServer{})
            v1.RegisterUserServiceServer(s, &UserServiceServer{})
        },
        grpcserver.WithServiceName("my-grpc-server"),
        grpcserver.WithMetadata(map[string]string{
            "version": "v1.0",
        }),
        // Set region info for Polaris compatibility
        grpcserver.WithRegionInfo("us-west-1", "us-west-1a", "campus-1"),
        grpcserver.WithEnvironment("production"),
        grpcserver.WithWeight(100),
        grpcserver.WithPriority(0),
        // Enable auto service discovery
        grpcserver.WithAutoServiceDiscovery(
            etcdv3.NewRegistry(&etcdv3.Config{
                Endpoints: []string{"127.0.0.1:2379"},
                Prefix:    "/beauty",
                TTL:       10,
            }),
        ),
    )
    
    // Create application
    app := beauty.New(
        beauty.WithService(grpcServer),
    )
    
    app.Start(context.Background())
}
```

### Multi-Registry Support

```go
grpcServer := grpcserver.New(
    ":58080",
    func(s *grpcpkg.Server) {
        v1.RegisterGreeterServer(s, &GreeterServer{})
        v1.RegisterUserServiceServer(s, &UserServiceServer{})
    },
    grpcserver.WithServiceName("my-grpc-server"),
    grpcserver.WithAutoServiceDiscovery(
        // Register to etcd
        etcdv3.NewRegistry(&etcdv3.Config{
            Endpoints: []string{"127.0.0.1:2379"},
            Prefix:    "/beauty",
        }),
        // Register to nacos
        nacos.NewRegistry(&nacos.Config{
            Addr:      []string{"127.0.0.1:8848"},
            Namespace: "default",
            Group:     "DEFAULT_GROUP",
        }),
        // Register to polaris
        polaris.NewRegistry(&polaris.Config{
            Addresses: []string{"127.0.0.1:8091"},
            Namespace: "default",
        }),
    ),
)
```

## Service Information in the Registry

After enabling auto service discovery, the following services appear in the registry:

### Service List
- `v1alpha.Greeter` - Greeter service (includes SayHello method)
- `v1alpha.UserService` - UserService (includes CreateUser, GetUser, and other methods)

### Service Metadata
Each service includes the following metadata:
- `kind`: "grpc"
- `methods`: Method list, e.g. `["SayHello"]`
- `proto_file`: Proto file info, e.g. `"greeter.proto"`
- `region`: Region info, e.g. `"us-west-1"`
- `zone`: Availability zone info, e.g. `"us-west-1a"`
- `campus`: Campus info, e.g. `"campus-1"`
- `environment`: Environment info, e.g. `"production"`
- `weight`: Service weight, e.g. `"100"`
- `priority`: Service priority, e.g. `"0"`
- User-defined metadata (set via WithMetadata)

## Implementation Details

1. **Service discovery**: Uses gRPC's built-in `GetServiceInfo()` method to retrieve registered service information
2. **Information parsing**: Parses service name, method list, proto file info, and other metadata
3. **Service wrapping**: Creates an independent `ProtoServiceWrapper` instance for each protobuf service
4. **Batch registration**: Registers each service separately to all configured registries

## Comparison with the Traditional Approach

### Traditional Approach
```go
// Register the entire gRPC server as a single service
app := beauty.New(
    beauty.WithService(grpcServer),
    beauty.WithRegistry(etcdRegistry), // Register the entire server
)
```

### Auto Service Discovery Approach
```go
// Each protobuf service is registered as an independent service
app := beauty.New(
    beauty.WithService(grpcServer),
    // No global registration needed; auto service discovery handles it
)
```

## Notes

1. **Service names**: Uses protobuf-defined service names as registered service names
2. **Metadata merging**: Server-level metadata is merged with each service's metadata
3. **Error handling**: If service discovery fails, errors are logged but do not prevent server startup
4. **Performance impact**: Service discovery runs asynchronously and does not affect gRPC server startup performance

## Example Project

For a complete usage example, see `examples/grpc-service-discovery/main.go`.
