# gRPC Client Service Discovery

## Overview

This feature implements a gRPC client with service discovery, supporting automatic service discovery, load balancing, failover, and region filtering. The client automatically discovers service instances and connects and invokes according to configured strategies.

## Features

- **Automatic service discovery**: Automatically discovers service instances from the registry
- **Load balancing**: Supports multiple load balancing strategies (round robin, random, weighted, etc.)
- **Failover**: Supports automatic failover and retry mechanisms
- **Region filtering**: Supports service filtering based on region, availability zone, and environment
- **Connection management**: Automatically manages connection pools with health check support
- **Service watching**: Real-time service change monitoring with automatic connection updates

## Usage

### Basic Usage

```go
package main

import (
    "context"
    "log"
    
    "github.com/rushteam/beauty/pkg/client/grpcclient"
    "github.com/rushteam/beauty/pkg/service/discover/etcdv3"
)

func main() {
    // Create service discovery client
    discovery := etcdv3.NewRegistry(&etcdv3.Config{
        Endpoints: []string{"127.0.0.1:2379"},
        Prefix:    "/beauty",
    })
    
    // Create client factory
    factory := grpcclient.NewClientFactory(discovery)
    
    // Get client for a specific service
    greeterClient := factory.GetClient("v1alpha.Greeter")
    
    // Get connection and call service
    conn, err := greeterClient.GetClient(context.Background())
    if err != nil {
        log.Fatal(err)
    }
    
    // Use connection to call service methods
    // greeterClient := v1.NewGreeterClient(conn)
    // resp, err := greeterClient.SayHello(ctx, &v1.HelloRequest{Name: "World"})
}
```

### Advanced Usage - Client Manager

```go
// Create client manager with load balancing and failover support
manager := grpcclient.NewClientManager(discovery, "v1alpha.Greeter",
    grpcclient.WithLoadBalanceStrategy(grpcclient.RoundRobin),
    grpcclient.WithManagerRegionFilter(
        []string{"us-west-1"}, // Single region
        []string{"us-west-1a"}, // Single availability zone
        []string{"campus-1"}, // Single campus
        []string{"production"}, // Single environment
    ),
    grpcclient.WithHealthCheck(true, time.Second*30),
    grpcclient.WithFailover(true, 3, time.Second),
)

// Multi-select filter example
multiManager := grpcclient.NewClientManager(discovery, "v1alpha.Greeter",
    grpcclient.WithLoadBalanceStrategy(grpcclient.WeightedRoundRobin),
    grpcclient.WithManagerRegionFilter(
        []string{"us-west-1", "us-west-2"}, // Multiple regions
        []string{"us-west-1a", "us-west-2a"}, // Multiple availability zones
        []string{"campus-1", "campus-2"}, // Multiple campuses
        []string{"production"}, // Production environment only
    ),
    grpcclient.WithHealthCheck(true, time.Second*30),
    grpcclient.WithFailover(true, 3, time.Second),
)

// Start manager
ctx := context.Background()
if err := manager.Start(ctx); err != nil {
    log.Fatal(err)
}

// Call service method (automatic load balancing and failover)
err := manager.Call(ctx, "/v1alpha.Greeter/SayHello", req, resp)
if err != nil {
    log.Printf("call failed: %v", err)
}
```

### Region Filtering

```go
// Single region filter
usWestClient := factory.GetClient("v1alpha.Greeter",
    grpcclient.WithDiscoveryRegionFilter(
        []string{"us-west-1"},
        []string{"us-west-1a"},
        []string{"campus-1"},
        []string{"production"},
    ),
)

// Multiple regions, availability zones, campuses, and environments
multiRegionClient := factory.GetClient("v1alpha.Greeter",
    grpcclient.WithDiscoveryRegionFilter(
        []string{"us-west-1", "us-east-1"}, // Multiple regions
        []string{"us-west-1a", "us-east-1a"}, // Multiple availability zones
        []string{"campus-1", "campus-2"}, // Multiple campuses
        []string{"production", "staging"}, // Multiple environments
    ),
)

// Partial filter (region only, no other restrictions)
regionOnlyClient := factory.GetClient("v1alpha.Greeter",
    grpcclient.WithDiscoveryRegionFilter(
        []string{"us-west-1", "us-west-2"}, // Region restriction only
        []string{}, // No zone restriction
        []string{}, // No campus restriction
        []string{}, // No environment restriction
    ),
)
```

#### Get Service Information
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

## Load Balancing Strategies

### 1. Round Robin (RoundRobin)
```go
manager := grpcclient.NewClientManager(discovery, "service-name",
    grpcclient.WithLoadBalanceStrategy(grpcclient.RoundRobin),
)
```

### 2. Random (Random)
```go
manager := grpcclient.NewClientManager(discovery, "service-name",
    grpcclient.WithLoadBalanceStrategy(grpcclient.Random),
)
```

### 3. Weighted Round Robin (WeightedRoundRobin)
```go
manager := grpcclient.NewClientManager(discovery, "service-name",
    grpcclient.WithLoadBalanceStrategy(grpcclient.WeightedRoundRobin),
)
```

### 4. Least Connections (LeastConnections)
```go
manager := grpcclient.NewClientManager(discovery, "service-name",
    grpcclient.WithLoadBalanceStrategy(grpcclient.LeastConnections),
)
```

## Failover Configuration

```go
manager := grpcclient.NewClientManager(discovery, "service-name",
    grpcclient.WithFailover(true, 3, time.Second),
)
```

Parameters:
- `enabled`: Whether to enable failover
- `maxRetries`: Maximum retry count
- `retryDelay`: Retry interval

## Health Check Configuration

```go
manager := grpcclient.NewClientManager(discovery, "service-name",
    grpcclient.WithHealthCheck(true, time.Second*30),
)
```

Parameters:
- `enabled`: Whether to enable health checks
- `interval`: Check interval

## Connection Options Configuration

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

## Interceptor Configuration

```go
// Unary interceptor
manager := grpcclient.NewClientManager(discovery, "service-name",
    grpcclient.WithUnaryInterceptors(
        loggingInterceptor,
        tracingInterceptor,
    ),
)

// Stream interceptor
manager := grpcclient.NewClientManager(discovery, "service-name",
    grpcclient.WithStreamInterceptors(
        streamLoggingInterceptor,
    ),
)
```

## Complete Example

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
    // Create service discovery
    discovery := etcdv3.NewRegistry(&etcdv3.Config{
        Endpoints: []string{"127.0.0.1:2379"},
        Prefix:    "/beauty",
    })
    
    // Create client manager
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
    
    // Start manager
    ctx := context.Background()
    if err := manager.Start(ctx); err != nil {
        log.Fatal(err)
    }
    defer manager.Close()
    
    // Call service
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

## Notes

1. **Connection management**: The client automatically manages the connection pool; no manual connection closing required
2. **Service watching**: The client monitors service changes in real time and updates connections automatically
3. **Failover**: When failover is enabled, failed calls are automatically retried
4. **Region filtering**: Ensure the server registers with correct region information
5. **Resource cleanup**: Remember to call `Close()` when done to clean up resources

## Server Integration

Client region filtering must match region information registered by the server:

```go
// Set region info on server registration
grpcServer := grpcserver.New(
    ":58080",
    handler,
    grpcserver.WithRegionInfo("us-west-1", "us-west-1a", "campus-1"),
    grpcserver.WithEnvironment("production"),
    grpcserver.WithAutoServiceDiscovery(registry),
)

// Client filters services in the same region
client := factory.GetClient("v1alpha.Greeter",
    grpcclient.WithDiscoveryRegionFilter("us-west-1", "us-west-1a", "production"),
)
```

This allows the client to automatically discover and connect to service instances in the same region, enabling proximity-based access and load balancing.
