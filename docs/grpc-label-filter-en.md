# gRPC Label Filter

> 中文版: [grpc-label-filter.md](grpc-label-filter.md)

A general-purpose label filtering system modeled on the Kubernetes Label Selector, providing flexible and powerful filtering of service instances.

## Overview

`LabelFilter` provides functionality similar to Kubernetes label selectors, supporting:
- **Exact matching**: `matchLabels` key-value matching
- **Expression matching**: `matchExpressions` with multiple operators
- **Convenience methods**: shortcuts for common scenarios
- **Backward compatibility**: keeps the existing region filter API compatible

## Supported Operators

### Equality-Based Operators
- `=` / `FilterOpEquals`: exactly equal
- `!=` / `FilterOpNotEquals`: not equal

### Set-Based Operators
- `in` / `FilterOpIn`: value is in the given set
- `notin` / `FilterOpNotIn`: value is not in the given set
- `exists` / `FilterOpExists`: label exists
- `notexist` / `FilterOpNotExist`: label does not exist

## Basic Usage

### 1. Exact Match Filtering

```go
// Use WithMatchLabel to add a single exact match
filter := grpcclient.NewLabelFilter().
    WithMatchLabel("region", "us-west-1").
    WithMatchLabel("environment", "production")

client := factory.GetClient("v1alpha.UserService",
    grpcclient.WithDiscoveryLabelFilter(filter),
)

// Or use WithMatchLabels to add several at once
filter2 := grpcclient.NewLabelFilter().
    WithMatchLabels(map[string]string{
        "region":      "us-west-1",
        "environment": "production",
        "status":      "healthy",
    })
```

### 2. Convenience Methods (Region Filtering)

```go
// Use convenience methods for region filtering
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
// The existing region filter is still available; under the hood it is converted to FilterLabels
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
    WithExpression("tier", grpcclient.FilterOpIn, "frontend", "api").
    // version must not be deprecated or legacy
    WithExpression("version", grpcclient.FilterOpNotIn, "deprecated", "legacy").
    // must have the canary label
    WithExpression("canary", grpcclient.FilterOpExists).
    // must not have the maintenance label
    WithExpression("maintenance", grpcclient.FilterOpNotExist)
```

### 2. Complex Filtering Scenarios

```go
// Mix several kinds of filter conditions
complexFilter := grpcclient.NewLabelFilter().
    // exact matches
    WithMatchLabel("service", "user-service").
    WithMatchLabel("status", "healthy").
    // region filtering (convenience methods)
    WithRegionIn("us-west-1", "us-west-2").
    WithEnvironmentIn("production").
    // advanced expressions
    WithExpression("version", grpcclient.FilterOpIn, "v2.0", "v2.1", "v2.2").
    WithExpression("tier", grpcclient.FilterOpNotIn, "deprecated").
    WithExpression("feature-flag", grpcclient.FilterOpExists).
    WithExpression("maintenance", grpcclient.FilterOpNotExist)

client := factory.GetClient("v1alpha.UserService",
    grpcclient.WithDiscoveryLabelFilter(complexFilter),
)
```

### 3. Using It in the Client Manager

```go
// Use a label filter in the client manager
managerFilter := grpcclient.NewLabelFilter().
    WithMatchLabels(map[string]string{
        "service":     "order-service",
        "environment": "production",
    }).
    WithExpression("region", grpcclient.FilterOpIn, "us-west-1", "us-east-1").
    WithExpression("load", grpcclient.FilterOpNotEquals, "high").
    WithExpression("healthy", grpcclient.FilterOpExists)

manager := grpcclient.NewClientManager(discovery, "v1alpha.OrderService",
    grpcclient.WithLoadBalanceStrategy(grpcclient.WeightedRoundRobin),
    grpcclient.WithManagerLabelFilter(managerFilter),
    grpcclient.WithHealthCheck(true, time.Second*30),
    grpcclient.WithFailover(true, 3, time.Second),
)
```

## Filter String Representation

LabelFilter provides a `String()` method that converts the filter conditions into a readable string:

```go
filter := grpcclient.NewLabelFilter().
    WithMatchLabel("environment", "production").
    WithExpression("region", grpcclient.FilterOpIn, "us-west-1", "us-east-1").
    WithExpression("tier", grpcclient.FilterOpNotIn, "deprecated").
    WithExpression("canary", grpcclient.FilterOpExists)

fmt.Println(filter.String())
// Output: environment=production,region in (us-west-1,us-east-1),tier notin (deprecated),canary
```

## Fault Tolerance

When no service instance matches the filter conditions, the system will:
1. Log a warning showing the filter conditions
2. Return all available service instances (fault-tolerance fallback)
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
        WithExpression("tier", grpcclient.FilterOpIn, "frontend", "api").
        WithExpression("deprecated", grpcclient.FilterOpNotExist),
)
```

## Best Practices

1. **Prefer convenience methods**: for common region filtering, use convenience methods such as `WithRegionIn`
2. **Use expressions judiciously**: use `WithExpression` for complex conditions that need more flexibility
3. **Mix and match**: exact matches and expression matches can be used together
4. **Mind performance**: the more complex the filter conditions, the higher the overhead
5. **Design for fault tolerance**: rely on the system's fault-tolerance fallback to ensure service availability
