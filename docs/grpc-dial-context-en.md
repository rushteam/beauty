# gRPC DialContext Simplified API

A concise functional dial API that lets users leverage service discovery just like native gRPC.

## Overview

`DialContext` wraps service discovery, load balancing, routing filters, and circuit breaking in a functional API:
dial by target (`scheme://service?labels`) without manually assembling resolver/balancer components.

## Usage Example

```go
conn, err := grpcclient.DialContext(ctx, "beauty://v1alpha.Greeter?env=production",
    grpcclient.WithGRPCDialOptions(grpc.WithTransportCredentials(insecure.NewCredentials())),
    grpcclient.WithDisableRouter(),
)
```

## Supported Target Formats

### 1. Beauty Protocol (Recommended)
```go
// Basic usage
"beauty://serviceName"

// With environment parameter
"beauty://serviceName?env=production"

// Multi-parameter filtering
"beauty://serviceName?env=production&region=us-west-1&tier=frontend"
```

### 2. Direct Registry Specification
```go
// Using etcd
"etcd://127.0.0.1:2379/serviceName"

// Using nacos
"nacos://127.0.0.1:8848/serviceName?namespace=production&group=DEFAULT_GROUP"
```

## Basic Usage

### Simplest Usage

```go
import "github.com/rushteam/beauty/pkg/client/grpcclient"

// Simplest dial
conn, err := grpcclient.DialContext(ctx, "beauty://v1alpha.UserService")
if err != nil {
    return err
}
defer conn.Close()

// Use directly
client := pb.NewUserServiceClient(conn)
resp, err := client.GetUser(ctx, &pb.GetUserRequest{Id: "123"})
```

### Dial with Parameters

```go
// With environment filter
conn, err := grpcclient.DialContext(ctx, "beauty://v1alpha.UserService?env=production")

// Multi-parameter filtering
conn, err := grpcclient.DialContext(ctx, 
    "beauty://v1alpha.UserService?env=production&region=us-west-1&tier=frontend")
```

### No-Context Variant

```go
// Uses default context.Background()
conn, err := grpcclient.Dial("beauty://v1alpha.UserService?env=production")
```

## Advanced Usage

### Custom Registry

```go
// Use a custom etcd registry
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

### Advanced Label Filtering

```go
// Use a complex label selector
labelFilter := grpcclient.NewLabelFilter().
    WithMatchLabel("environment", "production").
    WithExpression("tier", selector.FilterOpIn, "frontend", "api").
    WithExpression("deprecated", selector.FilterOpNotExist)

conn, err := grpcclient.DialContext(ctx, "beauty://v1alpha.UserService",
    grpcclient.WithLabelFilter(labelFilter),
    grpcclient.WithLoadBalancer("weighted_round_robin"),
)
```

### Custom gRPC Options

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

## Options Reference

### Core Options

#### `WithRegistry(discover.Discovery)`
Set a custom service registry.

```go
grpcclient.WithRegistry(etcdRegistry)
```

#### `WithLabelFilter(*ServiceLabelFilter)`
Set an advanced label filter.

```go
filter := grpcclient.NewLabelFilter().
    WithMatchLabel("env", "production").
    WithRegionIn("us-west-1", "us-west-2")

grpcclient.WithLabelFilter(filter)
```

#### `WithGRPCDialOptions(...grpc.DialOption)`
Set native gRPC connection options.

```go
grpcclient.WithGRPCDialOptions(
    grpc.WithTransportCredentials(insecure.NewCredentials()),
)
```

### Convenience Options

#### `WithTimeout(time.Duration)`
Set connection timeout.

```go
grpcclient.WithTimeout(time.Second * 5)
```

#### `WithEnvironment(string)`
Set environment filter (convenience method).

```go
grpcclient.WithEnvironment("production")
```

#### `WithRegion(string)`
Set region filter (convenience method).

```go
grpcclient.WithRegion("us-west-1")
```

#### `WithLoadBalancer(string)`
Set load balancing strategy.

```go
grpcclient.WithLoadBalancer("round_robin")     // Round robin
grpcclient.WithLoadBalancer("weighted_random") // Weighted random
grpcclient.WithLoadBalancer("p2c_ewma")        // P2C EWMA
```

### Backward-Compatible Options

#### `WithRegionFilter(regions, zones, campuses, environments []string)`
Use the legacy region filter (backward compatible).

```go
grpcclient.WithRegionFilter(
    []string{"us-west-1", "us-west-2"}, // regions
    []string{"us-west-1a"},             // zones
    []string{"campus-1"},               // campuses
    []string{"production"},             // environments
)
```

## URL Parameter Support

### Environment Parameters
```go
"beauty://service?env=production"
"beauty://service?environment=production"
```

### Region Parameters
```go
"beauty://service?region=us-west-1"
"beauty://service?region=us-west-1,us-west-2"  // Multiple regions
"beauty://service?zone=us-west-1a"
"beauty://service?campus=campus-1"
```

### Custom Labels
```go
"beauty://service?tier=frontend&version=v1.0&custom-label=value"
```

### Registry Parameters
```go
// nacos-specific parameters
"nacos://127.0.0.1:8848/service?namespace=production&group=DEFAULT_GROUP"
```

## Complete Examples

### Basic Service Call

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

    // Dial connection
    conn, err := grpcclient.DialContext(ctx, 
        "beauty://v1alpha.UserService?env=production&region=us-west-1")
    if err != nil {
        log.Fatal(err)
    }
    defer conn.Close()

    // Create client
    client := pb.NewUserServiceClient(conn)

    // Call service
    resp, err := client.GetUser(ctx, &pb.GetUserRequest{
        Id: "user-123",
    })
    if err != nil {
        log.Fatal(err)
    }

    log.Printf("User: %+v", resp)
}
```

### Advanced Configuration Example

```go
// Use custom registry and complex filter
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

## Comparison with Other APIs

| Feature | DialContext | Factory | Manager |
|------|-------------|---------|---------|
| **Ease of use** | Simplest | Moderate | Complex |
| **Connection management** | Single connection | Connection pool | Advanced management |
| **Load balancing** | gRPC built-in | Basic support | Advanced strategies |
| **Health checks** | gRPC built-in | Basic support | Full support |
| **Failover** | Manual implementation required | Basic support | Full support |
| **Use case** | Simple calls | General applications | Complex governance |

## Best Practices

### 1. Choose the Right API
- **Simple service calls**: Use `DialContext`
- **Connection reuse needed**: Use `Factory`
- **Complex governance requirements**: Use `Manager`

### 2. Error Handling
```go
conn, err := grpcclient.DialContext(ctx, target)
if err != nil {
    // Handle connection error
    return fmt.Errorf("failed to connect to %s: %w", target, err)
}
defer conn.Close()
```

### 3. Timeout Control
```go
// Set reasonable timeout
ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
defer cancel()

conn, err := grpcclient.DialContext(ctx, target,
    grpcclient.WithTimeout(time.Second*5), // Connection timeout
)
```

### 4. Production Configuration
```go
// Recommended production configuration
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

## Migration Guide

### Migrating from Factory to DialContext

**Before**:
```go
factory := grpcclient.NewClientFactory(discovery)
client := factory.GetClient("v1alpha.UserService")
conn, err := client.GetClient(ctx)
```

**After**:
```go
conn, err := grpcclient.DialContext(ctx, "beauty://v1alpha.UserService")
```

### Migrating from Traditional gRPC

**Before**:
```go
conn, err := grpc.NewClient("localhost:8080", grpc.WithTransportCredentials(insecure.NewCredentials()))
```

**After**:
```go
conn, err := grpcclient.DialContext(ctx, "beauty://v1alpha.UserService",
    grpcclient.WithGRPCDialOptions(grpc.WithTransportCredentials(insecure.NewCredentials())),
)
```

This preserves native gRPC simplicity while gaining powerful service discovery capabilities.
