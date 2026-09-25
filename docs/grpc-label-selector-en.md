# gRPC Label Selector

> 中文版: [grpc-label-selector.md](grpc-label-selector.md)

A general-purpose label filtering system modeled on the Kubernetes Label Selector, providing flexible and powerful filtering of service instances.

## Architecture

### Generic Label Selector (`pkg/utils/selector`)
- `LabelSelector`: the core label selector struct, aligned with the Kubernetes standard
- `LabelFilter`: a general-purpose label filter offering various operators and convenience methods
- `FilterOperator`: constants for the supported filter operators

### gRPC Service Label Filter (`pkg/client/grpcclient`)
- `ServiceLabelFilter`: a service-specific filter built on the generic `LabelFilter`
- Provides fault-tolerance and logging related to service discovery

## Overview

The label selector system provides functionality similar to Kubernetes label selectors, supporting:
- **Exact matching**: `matchLabels` key/value matching
- **Expression matching**: `matchExpressions` with multiple operators
- **Convenience methods**: shortcuts for common scenarios
- **Reusability**: can be reused across multiple modules
- **Backward compatibility**: keeps the existing region filter API compatible

## Supported Operators

### Equality-Based Operators
- `=` / `selector.FilterOpEquals`: exactly equal
- `!=` / `selector.FilterOpNotEquals`: not equal

### Set-Based Operators
- `in` / `selector.FilterOpIn`: value is in the given set
- `notin` / `selector.FilterOpNotIn`: value is not in the given set
- `exists` / `selector.FilterOpExists`: label exists
- `notexist` / `selector.FilterOpNotExist`: label does not exist

## Using the Generic Label Selector

### Using the Generic Selector Directly

```go
import "github.com/rushteam/beauty/pkg/utils/selector"

// create a label selector
filter := selector.NewLabelFilter().
    WithMatchLabel("environment", "production").
    WithExpression("region", selector.FilterOpIn, "us-west-1", "us-east-1")

// check whether a single object's labels match
labels := map[string]string{
    "environment": "production",
    "region":      "us-west-1",
    "tier":        "frontend",
}

if filter.Matches(labels) {
    fmt.Println("Labels match the selector")
}

// filter a list of labeled objects
items := []MyObject{...}
filtered := filter.FilterMap(items, func(item interface{}) map[string]string {
    obj := item.(MyObject)
    return obj.Labels // return the object's labels
})
```

## Filtering gRPC Services

### 1. Exact-Match Filtering

```go
// use WithMatchLabel to add a single exact match
filter := grpcclient.NewLabelFilter().
    WithMatchLabel("region", "us-west-1").
    WithMatchLabel("environment", "production")

client := factory.GetClient("v1alpha.UserService",
    grpcclient.WithDiscoveryLabelFilter(filter),
)

// or use WithMatchLabels to add several at once
filter2 := grpcclient.NewLabelFilter().
    WithMatchLabels(map[string]string{
        "region":      "us-west-1",
        "environment": "production",
        "status":      "healthy",
    })
```

### 2. Convenience Methods (Region Filtering)

```go
// use convenience methods for region filtering
filter := grpcclient.NewLabelFilter().
    WithRegionIn("us-west-1", "us-west-2").
    WithZoneIn("us-west-1a", "us-west-2a").
    WithCampusIn("campus-1").
    WithEnvironmentIn("production", "staging")

client := factory.GetClient("v1alpha.UserService",
    grpcclient.WithDiscoveryLabelFilter(filter),
)
```

### 3. Backward-Compatible Region Filter

```go
// the existing region filter still works; it is converted to FilterLabels internally
client := factory.GetClient("v1alpha.UserService",
    grpcclient.WithDiscoveryRegionFilter(
        []string{"us-west-1", "us-west-2"}, // regions
        []string{"us-west-1a"},             // zones
        []string{"campus-1"},               // campuses
        []string{"production"},             // environments
    ),
)
```

## Advanced Usage

### 1. Using Expression Operators

```go
filter := grpcclient.NewLabelFilter().
    // service tier must be frontend or api
    WithExpression("tier", selector.FilterOpIn, "frontend", "api").
    // version must not be deprecated or legacy
    WithExpression("version", selector.FilterOpNotIn, "deprecated", "legacy").
    // must have the canary label
    WithExpression("canary", selector.FilterOpExists).
    // must not have the maintenance label
    WithExpression("maintenance", selector.FilterOpNotExist)
```

### 2. Complex Filtering Scenarios

```go
// combine multiple kinds of filter conditions
complexFilter := grpcclient.NewLabelFilter().
    // exact match
    WithMatchLabel("service", "user-service").
    WithMatchLabel("status", "healthy").
    // region filtering (convenience methods)
    WithRegionIn("us-west-1", "us-west-2").
    WithEnvironmentIn("production").
    // advanced expressions
    WithExpression("version", selector.FilterOpIn, "v2.0", "v2.1", "v2.2").
    WithExpression("tier", selector.FilterOpNotIn, "deprecated").
    WithExpression("feature-flag", selector.FilterOpExists).
    WithExpression("maintenance", selector.FilterOpNotExist)

client := factory.GetClient("v1alpha.UserService",
    grpcclient.WithDiscoveryLabelFilter(complexFilter),
)
```

### 3. Using It in the Client Manager

```go
// use a label filter in the client manager
managerFilter := grpcclient.NewLabelFilter().
    WithMatchLabels(map[string]string{
        "service":     "order-service",
        "environment": "production",
    }).
    WithExpression("region", selector.FilterOpIn, "us-west-1", "us-east-1").
    WithExpression("load", selector.FilterOpNotEquals, "high").
    WithExpression("healthy", selector.FilterOpExists)

manager := grpcclient.NewClientManager(discovery, "v1alpha.OrderService",
    grpcclient.WithLoadBalanceStrategy(grpcclient.WeightedRoundRobin),
    grpcclient.WithManagerLabelFilter(managerFilter),
    grpcclient.WithHealthCheck(true, time.Second*30),
    grpcclient.WithFailover(true, 3, time.Second),
)
```

## String Representation of Filters

LabelFilter provides a `String()` method that converts the filter conditions into a readable string:

```go
filter := grpcclient.NewLabelFilter().
    WithMatchLabel("environment", "production").
    WithExpression("region", selector.FilterOpIn, "us-west-1", "us-east-1").
    WithExpression("tier", selector.FilterOpNotIn, "deprecated").
    WithExpression("canary", selector.FilterOpExists)

fmt.Println(filter.String())
// output: environment=production,region in (us-west-1,us-east-1),tier notin (deprecated),canary
```

## Fault Tolerance

When no service instance matches the filter conditions, the system will:
1. Log a warning that shows the filter conditions
2. Return all available service instances (fallback)
3. Ensure service availability

## API Comparison

### Old API (still supported)
```go
grpcclient.WithDiscoveryRegionFilter(
    []string{"us-west-1"}, []string{"us-west-1a"}, 
    []string{"campus-1"}, []string{"production"},
)
```

### New API
```go
grpcclient.WithDiscoveryLabelFilter(
    grpcclient.NewLabelFilter().
        WithRegionIn("us-west-1").
        WithZoneIn("us-west-1a").
        WithCampusIn("campus-1").
        WithEnvironmentIn("production"),
)
```

### Advanced New API
```go
grpcclient.WithDiscoveryLabelFilter(
    grpcclient.NewLabelFilter().
        WithMatchLabel("region", "us-west-1").
        WithExpression("tier", selector.FilterOpIn, "frontend", "api").
        WithExpression("deprecated", selector.FilterOpNotExist),
)
```

## Best Practices

1. **Prefer convenience methods**: for common region filtering, use convenience methods such as `WithRegionIn`
2. **Use expressions judiciously**: for complex conditions, `WithExpression` offers more flexibility
3. **Mix and match**: exact matching and expression matching can be used together
4. **Mind performance**: the more complex the filter conditions, the higher the overhead
5. **Design for fault tolerance**: rely on the system's fallback mechanism to ensure service availability
