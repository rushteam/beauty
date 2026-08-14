# Cross-Service Interop: Non-Beauty Services Calling Beauty gRPC Services

## Overview

gRPC services registered by Beauty are standard gRPC services — given a `host:port`, **any language or framework's
gRPC client can call them directly**, exactly like calling any other gRPC service.

The only key question is: **how to obtain the address (service discovery)**. Interoperability and convenience vary depending on the registry backend.

---

## Registry Interoperability Comparison

| Registry | Non-Beauty Client Discovery | Interop Difficulty | Reason |
|---|---|---|---|
| **Nacos** | Nacos native SDK (Java/Go/Python/…) | Easy | Beauty uses the standard Nacos registration API |
| **Consul** | Consul HTTP API or native SDK | Easy | Beauty uses the standard Consul Agent registration |
| **Polaris** | Polaris native SDK | Easy | Same as above |
| **Kubernetes** | Standard EndpointSlice / Service DNS | Easy | Native K8s mechanism |
| **etcd** | Must understand Beauty's key format | Medium | Beauty uses custom key paths and JSON format |

**Recommendation**: Nacos / Consul are the most cross-language, cross-framework friendly choices.

---

## What Beauty Registers

When using `grpcserver.WithAutoServiceDiscovery`, **each protobuf service is registered as an independent instance**,
with the service name set to the protobuf fully qualified name (e.g. `v1alpha.Greeter`).

Registered instance data structure:

```json
{
  "id":   "6bf14822-755d-4571-a7f5-bfe336783742",
  "kind": "grpc",
  "name": "v1alpha.Greeter",
  "addr": "10.0.0.5:58080",
  "metadata": {
    "kind":        "grpc",
    "methods":     "[\"SayHello\"]",
    "environment": "production",
    "region":      "us-west-1",
    "zone":        "us-west-1a",
    "weight":      "100",
    "version":     "v1.0"
  }
}
```

**Important fields**:
- `kind` = `"grpc"` — Beauty clients only discover instances with kind grpc; reverse registration must include this too
- `name` — protobuf fully qualified service name (not the name set by `WithServiceName`)
- `addr` — `host:port`, directly usable for gRPC dialing

---

## Scenario 1: Go Service Using Only the Beauty Client (Recommended, Easiest)

Go services do not need to import the entire Beauty framework — **only `pkg/client/grpcclient`** — service discovery,
load balancing, and label routing are all built in; no need to handle registry protocols and data formats yourself:

```go
import "github.com/rushteam/beauty/pkg/client/grpcclient"

// Option 1: registry address in URL, auto-constructed (zero config)
conn, err := grpcclient.DialContext(ctx, "etcd://127.0.0.1:2379/v1alpha.Greeter")

// Option 2: nacos / consul work the same way
conn, err := grpcclient.DialContext(ctx, "nacos://127.0.0.1:8848/v1alpha.Greeter?namespace=production")

// Option 3: beauty:// + explicit registry (with optional label filtering)
conn, err := grpcclient.DialContext(ctx, "beauty://v1alpha.Greeter?env=production",
    grpcclient.WithRegistry(etcdRegistry),
    grpcclient.WithLoadBalancer("p2c_ewma"),
)
```

This only depends on `pkg/client` and `pkg/service/discover`, without introducing Beauty core's `app.Start` lifecycle.
Once you have `*grpc.ClientConn`, it works exactly like native gRPC:

```go
client := pb.NewGreeterClient(conn)
resp, err := client.SayHello(ctx, &pb.HelloRequest{Name: "beauty"})
```

### Optional: Standard gRPC Resolver (Lighter Weight)

Beauty also provides optional gRPC resolver plugins; blank import registers them into the standard gRPC resolver pipeline,
supporting multi-instance load balancing and real-time failover:

```go
import (
    _ "github.com/rushteam/beauty/pkg/client/grpcclient/resolver/etcd"
    "google.golang.org/grpc"
)

// Standard gRPC API, native gRPC multi-instance load balancing
conn, err := grpc.NewClient(
    "etcd:///127.0.0.1:2379/v1alpha.Greeter?prefix=beauty",
    grpc.WithTransportCredentials(insecure.NewCredentials()),
    grpc.WithDefaultServiceConfig(`{"loadBalancingPolicy":"round_robin"}`),
)
```

Available resolver plugins:

| Plugin | import path | target format |
|---|---|---|
| etcd | `resolver/etcd` | `etcd:///host:port/service?prefix=beauty` |
| nacos | `resolver/nacos` | `nacos:///host:port/service?namespace=...` |
| consul | `resolver/consul` | `consul:///host:port/service` |

---

## Scenario 2: Non-Go Languages / No Beauty Code at All

If the caller is Java, Python, or another non-Go language, or a Go service that does not want any Beauty dependency,
use the registry's native SDK to query addresses, then make standard gRPC calls.

### Nacos Backend (Recommended, Most Cross-Language Friendly)

Beauty side:

```go
app := beauty.New(
    beauty.WithService(grpcserver.New(":9090",
        func(s *grpc.Server) { pb.RegisterGreeterServer(s, &greeter{}) },
        grpcserver.WithAutoServiceDiscovery(
            nacos.NewRegistry(&nacos.Config{
                Addr:      []string{"127.0.0.1:8848"},
                Namespace: "production",
            }),
        ),
    )),
)
```

**Java client** (using Nacos native SDK):

```java
NamingService naming = NamingFactory.createNamingService("127.0.0.1:8848");

// Query by protobuf service name
List<Instance> instances = naming.selectHealthyInstances("v1alpha.Greeter");

// Filter kind=grpc (optional but recommended)
Instance target = instances.stream()
    .filter(i -> "grpc".equals(i.getMetadata().get("kind")))
    .filter(i -> "production".equals(i.getMetadata().get("environment")))
    .findFirst()
    .orElseThrow(() -> new RuntimeException("no healthy grpc instance"));

// Standard gRPC call
ManagedChannel channel = ManagedChannelBuilder
    .forAddress(target.getIp(), target.getPort())
    .usePlaintext()
    .build();
GreeterGrpc.GreeterBlockingStub stub = GreeterGrpc.newBlockingStub(channel);
HelloReply reply = stub.sayHello(HelloRequest.newBuilder().setName("world").build());
```

**Python client** (using nacos-sdk-python):

```python
import nacos
import grpc
import greeter_pb2, greeter_pb2_grpc

client = nacos.NacosClient("127.0.0.1:8848")
instances = client.list_naming_instance("v1alpha.Greeter")

# Take the first healthy instance
inst = next(h for h in instances["hosts"] if h["healthy"])
addr = f"{inst['ip']}:{inst['port']}"

# Standard gRPC call
channel = grpc.insecure_channel(addr)
stub = greeter_pb2_grpc.GreeterStub(channel)
response = stub.SayHello(greeter_pb2.HelloRequest(name="world"))
```

### Consul Backend

Beauty side:

```go
grpcserver.WithAutoServiceDiscovery(
    consul.NewRegistry(&consul.Config{Addr: "127.0.0.1:8500"}),
)
```

**Any language** (using Consul HTTP API):

```bash
# Query healthy instances
curl 'http://127.0.0.1:8500/v1/health/service/v1alpha.Greeter?passing=true'
```

The returned JSON's `Service.Address` + `Service.Port` is the gRPC address; `Service.Meta` contains
Beauty-registered metadata.

**Go client** (using Consul SDK):

```go
import "github.com/hashicorp/consul/api"

client, _ := api.NewClient(api.DefaultConfig())
entries, _, _ := client.Health().Service("v1alpha.Greeter", "", true, nil)

for _, entry := range entries {
    if entry.Service.Meta["kind"] == "grpc" {
        addr := fmt.Sprintf("%s:%d", entry.Service.Address, entry.Service.Port)
        // Standard gRPC dial
        conn, _ := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
        // ...
    }
}
```

### etcd Backend

Beauty etcd key format:

```
/{prefix}/{serviceName}/{instanceID}
```

Default prefix is `beauty` (configurable via `etcdv3.Config.Prefix`); value is the JSON shown above.

**Go client** (using etcd SDK):

```go
import clientv3 "go.etcd.io/etcd/client/v3"

client, _ := clientv3.New(clientv3.Config{Endpoints: []string{"127.0.0.1:2379"}})
resp, _ := client.Get(ctx, "/beauty/v1alpha.Greeter/", clientv3.WithPrefix())

for _, kv := range resp.Kvs {
    var info struct {
        Addr     string            `json:"addr"`
        Kind     string            `json:"kind"`
        Metadata map[string]string `json:"metadata"`
    }
    json.Unmarshal(kv.Value, &info)
    if info.Kind == "grpc" {
        // info.Addr is the gRPC address
        conn, _ := grpc.NewClient(info.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
    }
}
```

---

## Scenario 3: Non-Beauty Services Registering to Beauty's Registry

If you want a non-Beauty service to be discoverable by Beauty clients, ensure registration includes **`kind: grpc`** metadata:

### Nacos

```java
// Java service registering to Nacos
namingService.registerInstance("com.example.OrderService", "10.0.0.10", 9090,
    new HashMap<String, String>() {{
        put("kind", "grpc");           // required: Beauty clients filter on this
        put("environment", "production");
        put("weight", "100");
    }});
```

### Consul

```go
// Go service registering to Consul
reg := &api.AgentServiceRegistration{
    ID:      "order-svc-1",
    Name:    "com.example.OrderService",
    Address: "10.0.0.10",
    Port:    9090,
    Meta: map[string]string{
        "kind":        "grpc",        // required
        "environment": "production",
    },
}
agent.ServiceRegister(reg)
```

### etcd

```go
// Go service writing directly to etcd key
key := "/beauty/com.example.OrderService/order-svc-1"
value, _ := json.Marshal(map[string]interface{}{
    "id":   "order-svc-1",
    "kind": "grpc",                   // required
    "name": "com.example.OrderService",
    "addr": "10.0.0.10:9090",
    "metadata": map[string]string{
        "kind":        "grpc",
        "environment": "production",
    },
})
client.Put(ctx, key, string(value))
```

After that, Beauty clients can discover it:

```go
conn, err := grpcclient.DialContext(ctx, "beauty://com.example.OrderService?env=production",
    grpcclient.WithRegistry(etcdRegistry),
)
```

---

## Notes

### 1. Service Names Must Match

When using `WithAutoServiceDiscovery`, the registered service name is the **protobuf fully qualified name** (e.g. `v1alpha.Greeter`),
not the name set by `WithServiceName("my-grpc-server")`. Always query using the correct name.

```go
// Server registers the protobuf name
pb.RegisterGreeterServer(s, &greeter{})  // → registered as "v1alpha.Greeter"

// Client must also query using the protobuf name
conn, _ := grpcclient.DialContext(ctx, "beauty://v1alpha.Greeter")
```

### 2. `kind=grpc` Is Required

Beauty discovery clients only return instances where `kind == "grpc"`. Non-Beauty services must include this field when registering,
otherwise they will be ignored by Beauty clients.

### 3. Label Filtering Is Client-Side

Filters like `?env=production&region=us-west-1` are applied locally by Beauty clients via metadata matching,
not as server-side filtering by the registry. Non-Beauty clients needing the same filtering must implement metadata matching themselves.

### 4. `beauty://` Is Beauty-Specific Syntax Sugar

The `beauty://` scheme is not a standard gRPC resolver; it must be used with `grpcclient.WithRegistry()`.
Non-Go clients or Go clients not using the Beauty library should query addresses directly via the native registry SDK.

### 5. DialContext vs gRPC Resolver

| Feature | `grpcclient.DialContext` | gRPC resolver (blank import) |
|---|---|---|
| Instance selection | Application layer: pick one instance at dial time | gRPC layer: can switch on every RPC |
| Load balancing | Beauty built-in (WRR/P2C/…) | gRPC built-in (round_robin/…) |
| Multi-instance failover | Use `ServiceDiscoveryClient.Call()` | gRPC handles automatically |
| Best for | Simple calls, single connection | Long-lived connections, real-time failover |

### 6. TLS Configured Separately

Beauty does not embed TLS information in registration metadata. Clients must configure transport credentials (TLS / mTLS) independently,
or manage certificates via an external control plane such as xDS.

For SPIFFE/SPIRE workload identity, see the standalone module [`contrib/spire`](../contrib/spire): Workload API issues
X509-SVID, wired to `WithTLSConfig` / `WithGRPCDialOptions` / `resty.WithBaseTransport`, optionally mapping peer
SPIFFE ID to `auth.User` / `authz.Subject`.

---

## Summary: How to Choose

| Your Situation | Recommended Approach | Complexity |
|---|---|---|
| **Go service (recommended)** | `grpcclient.DialContext("etcd://...")` — one line | Lowest |
| Go service, want native gRPC LB | blank import `resolver/etcd` + `grpc.NewClient` | Low |
| Java / Python / other languages | Nacos/Consul native SDK to query address → standard gRPC call | Medium |
| Go service, no Beauty code at all | Query registry directly → get `addr` → `grpc.NewClient(addr)` | Medium |
| Existing xDS control plane | `grpcclient.DialContext("xds:///service")` | Low |
