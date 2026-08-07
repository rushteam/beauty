# 跨服务互通：非 Beauty 服务调用 Beauty gRPC 服务

## 概述

Beauty 注册的 gRPC 服务是标准的 gRPC 服务——只要拿到 `host:port`，**任何语言、任何框架的
gRPC 客户端都能直接调用**，和调用任何其他 gRPC 服务完全一样。

关键问题只在于：**怎么拿到地址（服务发现）**。根据注册中心后端不同，互通性和便捷程度有差异。

---

## 注册中心互通性对照表

| 注册中心 | 非 Beauty 客户端发现 | 互通难度 | 原因 |
|---|---|---|---|
| **Nacos** | 用 Nacos 原生 SDK（Java/Go/Python/…） | 简单 | Beauty 用的就是 Nacos 标准注册 API |
| **Consul** | 用 Consul HTTP API 或原生 SDK | 简单 | Beauty 用的就是 Consul 标准 Agent 注册 |
| **Polaris** | 用 Polaris 原生 SDK | 简单 | 同上 |
| **Kubernetes** | 标准 EndpointSlice / Service DNS | 简单 | K8s 原生机制 |
| **etcd** | 需了解 Beauty 的 key 格式 | 中等 | Beauty 自定义了 key 路径和 JSON 格式 |

**推荐**：Nacos / Consul 是跨语言、跨框架互通最友好的选择。

---

## Beauty 注册了什么

使用 `grpcserver.WithAutoServiceDiscovery` 时，**每个 protobuf service 作为独立实例注册**，
服务名为 protobuf 的全限定名（如 `v1alpha.Greeter`）。

注册的实例数据结构：

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

**重要字段**：
- `kind` = `"grpc"` — Beauty 客户端只发现 kind 为 grpc 的实例，反向注册时也必须带
- `name` — protobuf 全限定服务名（非 `WithServiceName` 设置的名字）
- `addr` — `host:port`，直接可用于 gRPC 拨号

---

## 场景一：Go 服务只引入 Beauty 客户端（推荐，最省事）

Go 服务不需要引入整个 Beauty 框架，**只需引入 `pkg/client/grpcclient`**——服务发现、
负载均衡、标签路由全部内置，不用自己处理注册中心协议和数据格式：

```go
import "github.com/rushteam/beauty/pkg/client/grpcclient"

// 方式一：URL 里带注册中心地址，自动构造（零配置）
conn, err := grpcclient.DialContext(ctx, "etcd://127.0.0.1:2379/v1alpha.Greeter")

// 方式二：nacos / consul 同理
conn, err := grpcclient.DialContext(ctx, "nacos://127.0.0.1:8848/v1alpha.Greeter?namespace=production")

// 方式三：beauty:// + 显式传入注册中心（可带标签过滤）
conn, err := grpcclient.DialContext(ctx, "beauty://v1alpha.Greeter?env=production",
    grpcclient.WithRegistry(etcdRegistry),
    grpcclient.WithLoadBalancer("p2c_ewma"),
)
```

这只依赖 `pkg/client` 和 `pkg/service/discover`，不引入 Beauty 核心的 `app.Start` 生命周期。
拿到 `*grpc.ClientConn` 后和原生 gRPC 完全一样：

```go
client := pb.NewGreeterClient(conn)
resp, err := client.SayHello(ctx, &pb.HelloRequest{Name: "beauty"})
```

### 可选：用标准 gRPC resolver（更轻量）

Beauty 还提供了可选的 gRPC resolver 插件，blank import 即可注册到标准 gRPC resolver 管道，
支持多实例负载均衡和实时 failover：

```go
import (
    _ "github.com/rushteam/beauty/pkg/client/grpcclient/resolver/etcd"
    "google.golang.org/grpc"
)

// 标准 gRPC API，gRPC 原生多实例负载均衡
conn, err := grpc.NewClient(
    "etcd:///127.0.0.1:2379/v1alpha.Greeter?prefix=beauty",
    grpc.WithTransportCredentials(insecure.NewCredentials()),
    grpc.WithDefaultServiceConfig(`{"loadBalancingPolicy":"round_robin"}`),
)
```

可用的 resolver 插件：

| 插件 | import 路径 | target 格式 |
|---|---|---|
| etcd | `resolver/etcd` | `etcd:///host:port/service?prefix=beauty` |
| nacos | `resolver/nacos` | `nacos:///host:port/service?namespace=...` |
| consul | `resolver/consul` | `consul:///host:port/service` |

---

## 场景二：非 Go 语言 / 不引入任何 Beauty 代码

如果调用方是 Java、Python 等非 Go 语言，或者 Go 服务不想引入任何 Beauty 依赖，
可以直接用注册中心原生 SDK 查询地址，然后标准 gRPC 调用。

### Nacos 后端（推荐，跨语言最友好）

Beauty 侧：

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

**Java 客户端**（用 Nacos 原生 SDK）：

```java
NamingService naming = NamingFactory.createNamingService("127.0.0.1:8848");

// 按 protobuf 服务名查询
List<Instance> instances = naming.selectHealthyInstances("v1alpha.Greeter");

// 过滤 kind=grpc（可选，但推荐）
Instance target = instances.stream()
    .filter(i -> "grpc".equals(i.getMetadata().get("kind")))
    .filter(i -> "production".equals(i.getMetadata().get("environment")))
    .findFirst()
    .orElseThrow(() -> new RuntimeException("no healthy grpc instance"));

// 标准 gRPC 调用
ManagedChannel channel = ManagedChannelBuilder
    .forAddress(target.getIp(), target.getPort())
    .usePlaintext()
    .build();
GreeterGrpc.GreeterBlockingStub stub = GreeterGrpc.newBlockingStub(channel);
HelloReply reply = stub.sayHello(HelloRequest.newBuilder().setName("world").build());
```

**Python 客户端**（用 nacos-sdk-python）：

```python
import nacos
import grpc
import greeter_pb2, greeter_pb2_grpc

client = nacos.NacosClient("127.0.0.1:8848")
instances = client.list_naming_instance("v1alpha.Greeter")

# 取第一个健康实例
inst = next(h for h in instances["hosts"] if h["healthy"])
addr = f"{inst['ip']}:{inst['port']}"

# 标准 gRPC 调用
channel = grpc.insecure_channel(addr)
stub = greeter_pb2_grpc.GreeterStub(channel)
response = stub.SayHello(greeter_pb2.HelloRequest(name="world"))
```

### Consul 后端

Beauty 侧：

```go
grpcserver.WithAutoServiceDiscovery(
    consul.NewRegistry(&consul.Config{Addr: "127.0.0.1:8500"}),
)
```

**任意语言**（用 Consul HTTP API）：

```bash
# 查询健康实例
curl 'http://127.0.0.1:8500/v1/health/service/v1alpha.Greeter?passing=true'
```

返回的 JSON 中 `Service.Address` + `Service.Port` 即为 gRPC 地址，`Service.Meta` 包含
Beauty 注册的 metadata。

**Go 客户端**（用 Consul SDK）：

```go
import "github.com/hashicorp/consul/api"

client, _ := api.NewClient(api.DefaultConfig())
entries, _, _ := client.Health().Service("v1alpha.Greeter", "", true, nil)

for _, entry := range entries {
    if entry.Service.Meta["kind"] == "grpc" {
        addr := fmt.Sprintf("%s:%d", entry.Service.Address, entry.Service.Port)
        // 标准 gRPC 拨号
        conn, _ := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
        // ...
    }
}
```

### etcd 后端

Beauty 在 etcd 中的 key 格式：

```
/{prefix}/{serviceName}/{instanceID}
```

默认 prefix 为 `beauty`（`etcdv3.Config.Prefix` 可配置），value 为上述 JSON。

**Go 客户端**（用 etcd SDK）：

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
        // info.Addr 即为 gRPC 地址
        conn, _ := grpc.NewClient(info.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
    }
}
```

---

## 场景三：非 Beauty 服务注册到 Beauty 的注册中心

如果希望一个非 Beauty 服务能被 Beauty 客户端发现，只需确保注册时带上 **`kind: grpc`** metadata：

### Nacos

```java
// Java 服务注册到 Nacos
namingService.registerInstance("com.example.OrderService", "10.0.0.10", 9090,
    new HashMap<String, String>() {{
        put("kind", "grpc");           // 必须：Beauty 客户端靠这个过滤
        put("environment", "production");
        put("weight", "100");
    }});
```

### Consul

```go
// Go 服务注册到 Consul
reg := &api.AgentServiceRegistration{
    ID:      "order-svc-1",
    Name:    "com.example.OrderService",
    Address: "10.0.0.10",
    Port:    9090,
    Meta: map[string]string{
        "kind":        "grpc",        // 必须
        "environment": "production",
    },
}
agent.ServiceRegister(reg)
```

### etcd

```go
// Go 服务直接写 etcd key
key := "/beauty/com.example.OrderService/order-svc-1"
value, _ := json.Marshal(map[string]interface{}{
    "id":   "order-svc-1",
    "kind": "grpc",                   // 必须
    "name": "com.example.OrderService",
    "addr": "10.0.0.10:9090",
    "metadata": map[string]string{
        "kind":        "grpc",
        "environment": "production",
    },
})
client.Put(ctx, key, string(value))
```

之后 Beauty 客户端就能发现：

```go
conn, err := grpcclient.DialContext(ctx, "beauty://com.example.OrderService?env=production",
    grpcclient.WithRegistry(etcdRegistry),
)
```

---

## 注意事项

### 1. 服务名要对准

使用 `WithAutoServiceDiscovery` 时，注册的服务名是 **protobuf 全限定名**（如 `v1alpha.Greeter`），
不是 `WithServiceName("my-grpc-server")` 设置的名字。查询时务必使用正确的名字。

```go
// 服务端注册的是 protobuf 名
pb.RegisterGreeterServer(s, &greeter{})  // → 注册为 "v1alpha.Greeter"

// 客户端查询也要用 protobuf 名
conn, _ := grpcclient.DialContext(ctx, "beauty://v1alpha.Greeter")
```

### 2. `kind=grpc` 是必须的

Beauty 的发现客户端只返回 `kind == "grpc"` 的实例。非 Beauty 服务注册时必须带这个字段，
否则会被 Beauty 客户端忽略。

### 3. 标签过滤是客户端侧的

`?env=production&region=us-west-1` 这种过滤是 Beauty 客户端在本地做的 metadata 匹配，
而非注册中心的服务端过滤。非 Beauty 客户端如需同样的过滤能力，需要自行实现 metadata 匹配逻辑。

### 4. `beauty://` 是 Beauty 专属语法糖

`beauty://` scheme 不是标准 gRPC resolver，必须配合 `grpcclient.WithRegistry()` 使用。
非 Go 客户端或不使用 Beauty 库的 Go 客户端应直接用原生注册中心 SDK 查询地址。

### 5. DialContext 与 gRPC resolver 的区别

| 特性 | `grpcclient.DialContext` | gRPC resolver（blank import） |
|---|---|---|
| 实例选择 | 应用层：拨号时选一个实例 | gRPC 层：每次 RPC 都可切换 |
| 负载均衡 | Beauty 内置（WRR/P2C/…） | gRPC 内置（round_robin/…） |
| 多实例 failover | 需用 `ServiceDiscoveryClient.Call()` | gRPC 自动处理 |
| 适用场景 | 简单调用、单次连接 | 长连接、需要实时 failover |

### 6. TLS 独立配置

Beauty 不在注册 metadata 中嵌入 TLS 信息。客户端需要独立配置传输凭证（TLS / mTLS），
或通过 xDS 等外部控制面管理证书。

SPIFFE/SPIRE 工作负载身份见独立模块 [`contrib/spire`](../contrib/spire):Workload API 发
X509-SVID,接到 `WithTLSConfig` / `WithGRPCDialOptions` / `resty.WithBaseTransport`,可选把对端
SPIFFE ID 映射到 `auth.User` / `authz.Subject`。

---

## 总结：如何选择

| 你的情况 | 推荐方案 | 复杂度 |
|---|---|---|
| **Go 服务（推荐）** | `grpcclient.DialContext("etcd://...")` — 一行搞定 | 最低 |
| Go 服务，想用 gRPC 原生 LB | blank import `resolver/etcd` + `grpc.NewClient` | 低 |
| Java / Python / 其他语言 | Nacos/Consul 原生 SDK 查地址 → 标准 gRPC 调用 | 中 |
| Go 服务，不想引入任何 Beauty 代码 | 直接查注册中心 → 拿 `addr` → `grpc.NewClient(addr)` | 中 |
| 已有 xDS 控制面 | `grpcclient.DialContext("xds:///service")` | 低 |
