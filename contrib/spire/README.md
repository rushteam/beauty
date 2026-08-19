# contrib/spire —— SPIFFE/SPIRE 工作负载身份(独立模块)

用 [SPIRE](https://spiffe.io/docs/latest/spire-about/) Workload API 给 beauty 服务发
[X509-SVID](https://github.com/spiffe/spiffe/blob/main/standards/X509-SVID.md),做服务间
**mTLS**,并把对端 SPIFFE ID 接到 `pkg/middleware/auth` / `pkg/api/authz`。

```bash
go get github.com/rushteam/beauty/contrib/spire@latest
```

## 分层

| 层 | 做什么 | 用什么 |
|---|---|---|
| 传输 | 证书出示 + 校验 | `Source.MTLS*Credentials` / `MTLS*Config` |
| 工作负载身份 | 对端是谁(SPIFFE ID) | `UnaryServerInterceptor` / `HTTPMiddleware` |
| 终端用户 | 登录用户 JWT/token | 现有 `pkg/middleware/auth`(正交,不替换) |

服务发现只给地址;**不要**把注册中心 metadata 当信任根。

## 前置

1. 节点上跑着 SPIRE Agent(或兼容 Workload API 的实现)
2. 环境变量 `SPIFFE_ENDPOINT_SOCKET`(如 `unix:///tmp/spire-agent/public/api.sock`),或 `spire.WithAddr(...)`

## 用法

```go
source, err := spire.Connect(ctx) // 或 spire.WithAddr("unix:///run/spire/agent.sock")
if err != nil { return err }
defer source.Close()

app := beauty.New(
    beauty.WithComponent(source), // 停机时自动 Close
    beauty.WithGrpcServer(":9090", register,
        grpcserver.WithGrpcServerOptions(source.ServerCredsOption(spire.AuthorizeAny())),
        grpcserver.WithGrpcServerUnaryInterceptor(
            spire.UnaryServerInterceptor(spire.WithAuthzSubject()),
        ),
    ),
    beauty.WithWebServer(":8443", mux,
        webserver.WithTLSConfig(source.MTLSServerConfig(spire.AuthorizeAny())),
        webserver.WithMiddleware(spire.HTTPMiddleware(spire.WithAuthzSubject())),
    ),
)

// gRPC 客户端
conn, err := grpcclient.DialContext(ctx, "etcd://127.0.0.1:2379/my.svc",
    grpcclient.WithGRPCDialOptions(source.DialCredsOption(spire.AuthorizeAny())),
)

// HTTP 客户端(核心 resty.WithBaseTransport)
client := resty.NewHTTPClient(resty.WithBaseTransport(source.HTTPTransport(spire.AuthorizeAny())))
```

### 授权策略

```go
spire.AuthorizeAny()                              // 任意合法 SVID
spire.MustAuthorizeID("spiffe://td/ns/a/sa/b")    // 精确 ID
a, _ := spire.AuthorizeOneOf("spiffe://td/a", "spiffe://td/b")
a, _ := spire.AuthorizeMemberOf("example.org")    // 信任域内任意
```

传输层 `Authorizer` 在握手时拒绝非法对端;应用层 interceptor 再把合法 ID 写入
`auth.User` / `authz.Subject`,可接 `contrib/casbin` / `contrib/openfga`。

## 与 xDS / Istio 的关系

若证书由 Istio/Envoy 控制面下发,用核心 `pkg/client/grpcclient/xds` + `xds.WithCredentials()`,
**不必**引入本包。本包面向应用进程直接调 Workload API 的场景(无 sidecar 或 sidecar 旁路)。

## 测试

```bash
cd contrib/spire && go test ./...
```

单测不依赖真实 SPIRE Agent(用自签 URI SAN 证书与伪造 peer context)。
集成验证需本地 Agent + 已注册的 workload entry。
