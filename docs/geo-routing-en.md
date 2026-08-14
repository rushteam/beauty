# Geo-Routing (Locality-Aware Routing)

## Overview

`LocalityRouter` provides locality-aware routing for service-to-service calls. It filters service instances by geographic proximity using a **tiered fallback** strategy: campus → zone → region. The closest matching tier is preferred; if no match is found at any tier, the behavior depends on the `globalFallback` setting.

This differs from `LabelRouter` (hard match: either match or fail-closed). `LocalityRouter` progressively relaxes the filter to maximize availability while preferring low-latency paths.

## How It Works

```
                    ┌──────────────────────────┐
                    │   All discovered nodes    │
                    └────────────┬─────────────┘
                                 │
                     ┌───────────▼──────────┐
                     │  Tier 1: campus match │──── hit ──▶ return matched
                     └───────────┬──────────┘
                                 │ miss
                     ┌───────────▼──────────┐
                     │  Tier 2: zone match   │──── hit ──▶ return matched
                     └───────────┬──────────┘
                                 │ miss
                     ┌───────────▼──────────┐
                     │  Tier 3: region match │──── hit ──▶ return matched
                     └───────────┬──────────┘
                                 │ miss
                     ┌───────────▼──────────┐
                     │  globalFallback=true? │
                     │  yes → return all     │
                     │  no  → return nil     │
                     └───────────────────────┘
```

## Prerequisites

### Server Side: Register Region Metadata

Each service instance must register its locality metadata. Use `WithRegionInfo` on the server:

```go
// gRPC server
grpcServer := grpcserver.New(":58080", handler,
    grpcserver.WithRegionInfo("cn-east", "cn-east-1a", "campus-1"),
    grpcserver.WithAutoServiceDiscovery(registry),
)

// HTTP server
webServer := webserver.New(":8080", handler,
    webserver.WithRegionInfo("cn-east", "cn-east-1a", "campus-1"),
    webserver.WithAutoServiceDiscovery(registry),
)
```

This writes three metadata keys to the service registration: `region`, `zone`, `campus`.

### Client Side: Declare Local Locality

Create a `LocalityRouter` with the caller's own locality:

```go
import "github.com/rushteam/beauty/pkg/governance/router"

localityRouter := router.NewLocalityRouter(router.Locality{
    Region: "cn-east",
    Zone:   "cn-east-1a",
    Campus: "campus-1",
})
```

## Usage

### Basic Usage

```go
import (
    "github.com/rushteam/beauty/pkg/client/grpcclient"
    "github.com/rushteam/beauty/pkg/governance/router"
)

// Declare the caller's locality
r := router.NewLocalityRouter(router.Locality{
    Region: "cn-east",
    Zone:   "cn-east-1a",
    Campus: "campus-1",
})

// Use with gRPC client
client := grpcclient.NewServiceDiscoveryClient(discovery, "payment",
    grpcclient.WithServiceRouter(r),
)
```

With this setup:
1. If there are `payment` instances in `campus-1` of `cn-east-1a`, those are returned.
2. If not, all instances in zone `cn-east-1a` are returned.
3. If not, all instances in region `cn-east` are returned.
4. If none match, `nil` is returned (fail-closed — the caller gets an error).

### Global Fallback (Prefer Availability)

```go
r := router.NewLocalityRouter(router.Locality{
    Region: "cn-east",
    Zone:   "cn-east-1a",
}, router.WithGlobalFallback(true))
```

When `globalFallback` is `true`, if no instance matches at any tier, **all** instances are returned instead of `nil`. This trades latency for availability — useful when cross-region calls are acceptable as a last resort.

### Custom Tiers

```go
// Skip campus, only use zone and region
r := router.NewLocalityRouter(local, router.WithTiers("zone", "region"))

// Add a custom "rack" tier (finest granularity)
r := router.NewLocalityRouter(local, router.WithTiers("rack", "campus", "zone", "region"))
```

Custom tiers change the fallback order and the metadata keys used for matching. Tiers are evaluated left-to-right (closest first).

### Combine with LabelRouter (ChainRouter)

`LocalityRouter` can be chained with other routers via `ChainRouter`. A common pattern is locality-first, then version filtering:

```go
import (
    "github.com/rushteam/beauty/pkg/governance/router"
    "github.com/rushteam/beauty/pkg/utils/selector"
)

locality := router.NewLocalityRouter(router.Locality{
    Region: "cn-east",
    Zone:   "cn-east-1a",
}, router.WithGlobalFallback(true))

versionFilter := router.NewLabelRouter(
    selector.NewLabelFilter().WithExpression("version", selector.FilterOpIn, "v2"),
)

chain := router.NewChainRouter(locality, versionFilter)

client := grpcclient.NewServiceDiscoveryClient(discovery, "payment",
    grpcclient.WithServiceRouter(chain),
)
```

This first narrows instances to the same zone/region, then picks only `v2` instances from that subset.

## API Reference

### `Locality` struct

| Field    | Type   | Description                          | Example         |
|----------|--------|--------------------------------------|-----------------|
| `Region` | string | Geographic region                    | `"cn-east"`     |
| `Zone`   | string | Availability zone within the region  | `"cn-east-1a"`  |
| `Campus` | string | Campus / data center within the zone | `"campus-1"`    |

Empty fields are skipped during tier matching.

### `NewLocalityRouter(local Locality, opts ...LocalityOption) *LocalityRouter`

Creates a locality-aware router. The `local` parameter declares the caller's own locality.

### Options

| Option                          | Default                          | Description                                                  |
|---------------------------------|----------------------------------|--------------------------------------------------------------|
| `WithGlobalFallback(bool)`      | `false`                          | Return all nodes when no tier matches (instead of nil)       |
| `WithTiers(keys ...string)`     | `["campus", "zone", "region"]`   | Custom fallback tier order and metadata keys                 |

### `Filter(serviceName string, nodes []ServiceInfo) []ServiceInfo`

Implements `ServiceRouter`. Filters nodes by tiered locality matching. Returns:
- Matched nodes from the closest tier, or
- All nodes if `globalFallback` is true and no tier matched, or
- `nil` if `globalFallback` is false and no tier matched.

## Design Decisions

1. **Stateless & concurrent-safe**: `LocalityRouter` holds only configuration; no mutable state. Safe for concurrent use across goroutines.
2. **Composable**: Works as a standalone router or inside `ChainRouter` with `LabelRouter` and custom routers.
3. **Zero impact when unused**: If `LocalityRouter` is not configured, the default `NoopRouter` passes all nodes through — no behavior change.
4. **Metadata convention**: Uses the same `region`/`zone`/`campus` keys as `grpcserver.WithRegionInfo` and `webserver.WithRegionInfo`.
