# gRPC xDS 客户端示例

通过 `xds:///service` 目标，让 gRPC 客户端从 **xDS 控制平面**（如 Istio/istiod、
或自建 go-control-plane）动态发现端点并应用其下发的负载均衡/路由策略。

这是「客户端消费 xDS」能力，区别于 beauty 自带的注册中心拉模型
（`etcd:// / nacos:// / consul:// / polaris:// / k8s://`）。

## 用法

```go
import (
    "github.com/rushteam/beauty/pkg/client/grpcclient"
    _ "github.com/rushteam/beauty/pkg/client/grpcclient/xds" // 注册 xds:// resolver
)

// 明文
conn, _ := grpcclient.Dial("xds:///my-service", grpcclient.WithInsecure())

// 安全（控制平面下发 mTLS，回退明文）
import beautyxds "github.com/rushteam/beauty/pkg/client/grpcclient/xds"
conn, _ := grpcclient.Dial("xds:///my-service", beautyxds.WithCredentials())
```

## 引导配置（必需）

gRPC 通过环境变量读取 xDS 引导文件，二选一：

```bash
export GRPC_XDS_BOOTSTRAP=$(pwd)/xds_bootstrap.example.json
# 或内联
export GRPC_XDS_BOOTSTRAP_CONFIG="$(cat xds_bootstrap.example.json)"
```

`xds_bootstrap.example.json` 为最小示例，需把 `server_uri` 改成你的控制平面地址。
在 Istio 环境中，该引导文件通常由 sidecar 注入自动生成，无需手写。

## 运行

```bash
export GRPC_XDS_BOOTSTRAP=$(pwd)/xds_bootstrap.example.json
go run .
```

## 说明

- xDS 模式下端点发现与负载均衡均由控制平面决定，因此 `WithRegistry` /
  `WithLoadBalancer` / 地域·版本标签过滤等选项**不生效**。
- 未设置引导配置时，首次发起 RPC 会报 xDS bootstrap 相关错误。
