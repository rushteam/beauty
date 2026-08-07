# spire-grpc 示例

演示 `contrib/spire` 接入 beauty gRPC:

1. Workload API → X509-SVID
2. 服务端 / 客户端 mTLS
3. 对端 SPIFFE ID → `auth.User`

## 前置

- 本机 SPIRE Agent 在跑
- 当前进程有 workload entry
- `SPIFFE_ENDPOINT_SOCKET` 已设置(或传 `-socket`)

## 运行

```bash
cd examples/spire-grpc

# 终端 1
go run . -mode=server

# 终端 2
go run . -mode=client -addr=127.0.0.1:9090
```
