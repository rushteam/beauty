# {{.Name}} gRPC 微服务

这是一个基于Beauty框架的gRPC微服务模板，包含完整的protobuf定义和代码生成配置。

## 🚀 快速开始

### 1. 安装依赖

```bash
# 安装buf工具
go install github.com/bufbuild/buf/cmd/buf@latest

# 安装项目依赖
go mod tidy
```

### 2. 生成protobuf代码

```bash
# 使用脚本生成
./scripts/generate.sh

# 或者直接使用buf命令
buf generate
```

### 3. 运行服务

```bash
go run main.go
```

## 📁 项目结构

```
.
├── api/                    # protobuf定义和生成的代码
│   └── v1/
│       ├── user.proto     # protobuf定义
│       ├── user.pb.go     # 生成的protobuf消息
│       ├── user_grpc.pb.go # 生成的gRPC服务
│       └── user.pb.gw.go  # 生成的gRPC-Gateway
├── internal/              # 内部代码
│   ├── config/           # 配置管理
│   ├── endpoint/         # 端点定义
│   │   └── grpc/         # gRPC服务注册
│   ├── infra/            # 基础设施
│   │   ├── conf/         # 配置加载
│   │   ├── logger/       # 日志
│   │   ├── middleware/   # 中间件
│   │   └── registry/     # 服务注册
│   └── service/          # 业务服务
│       └── user.go       # 用户服务实现
├── scripts/              # 脚本
│   └── generate.sh       # 代码生成脚本
├── config/               # 配置文件
│   └── dev/
│       └── app.yaml      # 应用配置
├── buf.yaml              # buf配置
├── buf.gen.yaml          # buf生成配置
├── buf.lock              # buf锁定文件
├── go.mod                # Go模块
├── go.sum                # Go依赖
└── main.go               # 主程序
```

## 🔧 开发指南

### 修改protobuf定义

1. 编辑 `api/v1/user.proto` 文件
2. 运行 `./scripts/generate.sh` 重新生成代码
3. 更新 `internal/service/user.go` 中的服务实现

### 添加新的gRPC服务

1. 在 `api/v1/` 目录下创建新的 `.proto` 文件
2. 在 `internal/service/` 目录下实现服务
3. 在 `internal/endpoint/grpc/server.go` 中注册服务
4. 运行 `./scripts/generate.sh` 生成代码

### 配置服务

编辑 `config/dev/app.yaml` 文件来配置服务参数：

```yaml
app: {{.Name}}
version: "1.0.0"

grpc:
  addr: ":9090"
  timeout: "30s"

# 其他配置...
```

## 🧪 测试服务

### 使用grpcurl测试

```bash
# 安装grpcurl
go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest

# 测试服务
grpcurl -plaintext localhost:9090 list
grpcurl -plaintext localhost:9090 api.v1.UserService/ListUsers
```

### 使用grpc-gateway测试HTTP接口

服务启动后，可以通过HTTP接口测试：

```bash
# 创建用户
curl -X POST http://localhost:8080/api/v1/users \
  -H "Content-Type: application/json" \
  -d '{"name": "Alice", "email": "alice@example.com"}'

# 获取用户列表
curl http://localhost:8080/api/v1/users
```

## 📚 相关文档

- [Beauty框架文档](../../README.md)
- [gRPC官方文档](https://grpc.io/docs/)
- [protobuf文档](https://developers.google.com/protocol-buffers)
- [buf文档](https://docs.buf.build/)
