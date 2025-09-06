# API命令Protobuf集成

## 概述

重构后的API命令现在支持protobuf解析，结合buf v2的能力来构建和管理protobuf文件。该功能提供了以下能力：

- 自动检测和解析protobuf文件
- 使用[grpc-gateway](https://github.com/grpc-ecosystem/grpc-gateway)官方包解析`google/api/annotations.proto`
- 集成buf v2工具链进行代码生成
- 向后兼容传统的api.spec文件格式
- 支持HTTP注解解析

## 功能特性

### 1. Protobuf文件解析
- 自动扫描项目目录中的.proto文件
- 使用[grpc-gateway](https://github.com/grpc-ecosystem/grpc-gateway)官方包解析服务定义、消息类型和RPC方法
- 正确解析`google/api/annotations.proto`中的HTTP注解
- 支持import依赖解析

### 2. Buf v2集成
- 自动生成buf.yaml和buf.gen.yaml配置文件
- 支持代码生成（Go、gRPC、grpc-gateway）
- 提供lint检查和格式化功能
- 支持破坏性变更检测

### 3. 向后兼容
- 如果protobuf解析失败，自动回退到传统api.spec格式
- 保持现有工作流程的连续性

## 使用方法

### 基本用法

```bash
# 解析protobuf项目
beauty api my-project

# 指定项目路径
beauty api my-project --path /path/to/project
```

### 项目结构

推荐的项目结构：

```
my-project/
├── api/
│   └── service.proto
├── buf.yaml          # 自动生成
├── buf.gen.yaml      # 自动生成
└── api/v1/           # 生成的代码
    ├── service.pb.go
    ├── service_grpc.pb.go
    └── service.pb.gw.go
```

### 示例项目

项目包含一个完整的示例：`examples/protobuf-example/`，展示了如何使用protobuf定义服务，包括HTTP选项。

### Protobuf文件示例

```protobuf
syntax = "proto3";

package v1alpha;

option go_package = "api/v1";

import "google/api/annotations.proto";

service UserService {
  rpc CreateUser (CreateUserRequest) returns (CreateUserResponse) {
    option (google.api.http) = {
      post: "/v1/users"
      body: "*"
    };
  }
}

message CreateUserRequest {
  string name = 1;
  string email = 2;
}

message CreateUserResponse {
  string id = 1;
  string name = 2;
  string email = 3;
}
```

## 配置说明

### buf.yaml配置

```yaml
version: v2
name: buf.build/example/myrepo
deps:
  - buf.build/googleapis/googleapis
build:
  roots:
    - api
lint:
  use:
    - DEFAULT
breaking:
  use:
    - FILE
```

### buf.gen.yaml配置

```yaml
version: v2
managed:
  enabled: true
plugins:
  - name: go
    out: api/v1
    opt: paths=source_relative
  - name: go-grpc
    out: api/v1
    opt: paths=source_relative
  - name: grpc-gateway
    out: api/v1
    opt: paths=source_relative
```

## 输出示例

运行命令后的输出示例：

```
开始解析protobuf文件...
成功解析 1 个protobuf文件:

文件: api/service.proto
  包名: v1alpha
  Go包名: api/v1
  服务数量: 1
  消息数量: 4
  服务: UserService
    RPC: CreateUser(CreateUserRequest) -> CreateUserResponse
    RPC: GetUser(GetUserRequest) -> GetUserResponse
    RPC: UpdateUser(UpdateUserRequest) -> UpdateUserResponse
    RPC: DeleteUser(DeleteUserRequest) -> DeleteUserResponse
    RPC: ListUsers(ListUsersRequest) -> ListUsersResponse
  消息: CreateUserRequest
    字段: string name 1
    字段: string email 2
    字段: int32 age 3
  消息: CreateUserResponse
    字段: string id 1
    字段: string name 2
    字段: string email 3
    字段: int32 age 4
    字段: string created_at 5

开始生成代码...
代码生成完成!
```

## 依赖要求

- Go 1.19+
- buf CLI工具
- protoc编译器（通过buf自动管理）

## 安装buf

```bash
# macOS
brew install buf

# Linux/Windows
curl -sSL "https://github.com/bufbuild/buf/releases/latest/download/buf-$(uname -s)-$(uname -m)" -o "/usr/local/bin/buf"
chmod +x "/usr/local/bin/buf"
```

## 故障排除

### 常见问题

1. **buf未安装**
   ```
   错误: buf未安装，请先安装buf工具
   ```
   解决方案：按照上述安装说明安装buf

2. **protobuf文件语法错误**
   ```
   错误: protobuf文件检查失败
   ```
   解决方案：检查.proto文件的语法，使用`buf lint`命令验证

3. **依赖缺失**
   ```
   错误: 找不到google/api/annotations.proto
   ```
   解决方案：确保buf.yaml中包含了正确的依赖

### 调试模式

可以通过设置环境变量启用详细输出：

```bash
export BEAUTY_DEBUG=1
beauty api my-project
```

## 迁移指南

### 从传统api.spec迁移

1. 将现有的服务定义转换为protobuf格式
2. 创建.proto文件
3. 运行`beauty api`命令进行解析和代码生成
4. 更新项目配置以使用生成的代码

### 示例迁移

**传统格式 (api.spec):**
```
service UserService {
    @route POST "/users"
    rpc CreateUser(CreateUserRequest) returns (CreateUserResponse)
}
```

**Protobuf格式 (service.proto):**
```protobuf
service UserService {
  rpc CreateUser (CreateUserRequest) returns (CreateUserResponse) {
    option (google.api.http) = {
      post: "/users"
      body: "*"
    };
  }
}
```
