# 统一模板功能

## 概述

Beauty 框架现在支持统一模板功能，允许用户通过交互式方式选择要启用的服务类型，而不是使用分离的模板。这提供了更大的灵活性，用户可以在一个项目中同时启用 HTTP、gRPC 和定时任务服务。

## 功能特性

### 1. 交互式服务选择

当使用 `unified` 模板时，CLI 工具会提供交互式界面让用户选择要启用的服务类型：

```bash
beauty new my-project --template unified
```

交互界面会显示以下选项：
- HTTP 服务 (REST API)
- gRPC 服务 (高性能 RPC)
- 定时任务服务 (Cron Jobs)
- 全栈服务 (HTTP + gRPC + Cron)
- 自定义组合

### 2. 命令行参数支持

用户也可以通过命令行参数直接指定要启用的服务类型：

```bash
# 只启用 HTTP 服务
beauty new my-project --template unified --web

# 启用 HTTP 和 gRPC 服务
beauty new my-project --template unified --web --grpc

# 启用所有服务
beauty new my-project --template unified --web --grpc --cron
```

### 3. 智能文件生成

统一模板会根据用户选择的服务类型智能生成相应的文件：

- **HTTP 服务**: 生成 `internal/endpoint/handlers/` 和 `internal/endpoint/router/` 相关文件
- **gRPC 服务**: 生成 `api/`、`internal/endpoint/grpc/` 和 `internal/service/` 相关文件
- **定时任务服务**: 生成 `internal/job/` 相关文件

### 4. 条件编译

生成的代码使用 Go 模板的条件编译功能，只有在启用相应服务时才会包含相关代码：

```go
{{if .EnableWeb}}
// HTTP 服务相关代码
{{end}}

{{if .EnableGrpc}}
// gRPC 服务相关代码
{{end}}

{{if .EnableCron}}
// 定时任务服务相关代码
{{end}}
```

## 使用示例

### 示例 1: 创建全栈服务

```bash
beauty new my-fullstack-app --template unified
# 选择选项 4 (全栈服务)
```

### 示例 2: 创建 HTTP + gRPC 服务

```bash
beauty new my-api-service --template unified --web --grpc
```

### 示例 3: 只创建定时任务服务

```bash
beauty new my-cron-service --template unified --cron
```

## 项目结构

使用统一模板生成的项目结构如下：

```
my-project/
├── main.go                 # 主入口文件（根据服务类型条件编译）
├── go.mod                  # Go 模块文件（根据服务类型包含依赖）
├── config/
│   └── dev/
│       └── app.yaml        # 配置文件（根据服务类型包含配置）
├── api/                    # gRPC API 定义（仅当启用 gRPC 时）
│   └── v1/
│       └── user.proto
├── internal/
│   ├── config/
│   │   └── config.go       # 配置结构（根据服务类型包含字段）
│   ├── endpoint/
│   │   ├── handlers/       # HTTP 处理器（仅当启用 HTTP 时）
│   │   ├── router/         # HTTP 路由（仅当启用 HTTP 时）
│   │   └── grpc/           # gRPC 服务（仅当启用 gRPC 时）
│   ├── infra/              # 基础设施代码
│   ├── job/                # 定时任务（仅当启用 Cron 时）
│   └── service/            # 业务服务（仅当启用 gRPC 时）
└── scripts/                # 构建脚本（仅当启用 gRPC 时）
    └── generate.sh
```

## 配置说明

### HTTP 服务配置

```yaml
http:
  addr: ":8080"
  read_timeout: "30s"
  write_timeout: "30s"
  idle_timeout: "120s"
```

### gRPC 服务配置

```yaml
grpc:
  addr: ":9090"
  max_recv_msg_size: 4194304
  max_send_msg_size: 4194304
```

### 定时任务服务配置

定时任务服务使用默认配置，无需额外配置项。

## 向后兼容性

统一模板功能完全向后兼容现有的模板系统：

- `web-service`: 仍然可用，等同于 `unified --web`
- `grpc-service`: 仍然可用，等同于 `unified --grpc`
- `cron-service`: 仍然可用，等同于 `unified --cron`

## 最佳实践

1. **新项目推荐使用统一模板**: 使用 `--template unified` 获得最大的灵活性
2. **明确指定服务类型**: 在 CI/CD 环境中使用命令行参数而不是交互式选择
3. **按需启用服务**: 只启用项目实际需要的服务类型，避免不必要的依赖
4. **使用条件编译**: 在自定义代码中也使用条件编译来保持代码的整洁

## 技术实现

统一模板功能通过以下技术实现：

1. **Go 模板条件编译**: 使用 `{{if .EnableWeb}}` 等条件语句
2. **智能文件过滤**: 根据服务类型跳过不需要的文件
3. **交互式 CLI**: 使用 `bufio.Reader` 实现用户交互
4. **配置驱动**: 通过配置对象控制代码生成

这种设计确保了代码的简洁性和可维护性，同时提供了最大的灵活性。
