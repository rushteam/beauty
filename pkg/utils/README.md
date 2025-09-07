# 通用工具库 (Utils)

Beauty 框架的通用工具库，提供各种可复用的组件和工具函数。

## 概述

`pkg/utils` 包含了框架中各种通用的、可复用的组件，这些组件设计为独立的模块，可以在框架的不同部分或外部项目中使用。

## 组件列表

### 📋 [标签选择器 (selector)](./selector/)

基于 Kubernetes Label Selector 设计的通用标签过滤组件。

**主要功能:**
- 支持 Kubernetes 风格的标签选择器语法
- 提供精确匹配和表达式匹配
- 支持多种操作符（`=`, `!=`, `in`, `notin`, `exists`, `notexist`）
- 便捷的地域/环境过滤方法

**使用场景:**
- 服务发现中的实例过滤
- 配置管理中的配置选择
- 资源调度中的节点选择
- 任何需要基于标签进行过滤的场景

**快速示例:**
```go
import "github.com/rushteam/beauty/pkg/utils/selector"

filter := selector.NewLabelFilter().
    WithMatchLabel("environment", "production").
    WithExpression("region", selector.FilterOpIn, "us-west-1", "us-east-1")

if filter.Matches(labels) {
    // 标签匹配
}
```

### 🔧 [地址工具 (addr)](./addr/)

网络地址相关的工具函数。

**主要功能:**
- IP 地址解析和验证
- 端口处理
- 网络地址格式化

### 🌸 [布隆过滤器 (bloom)](./bloom/)

高效的布隆过滤器实现。

**主要功能:**
- 快速成员检测
- 内存高效
- 支持自定义哈希函数

### 🆔 [UUID 生成器 (uuid)](./uuid/)

UUID 生成和处理工具。

**主要功能:**
- 多种 UUID 版本支持
- 高性能生成
- 格式验证和转换

## 设计原则

### 1. **独立性**
每个工具组件都应该是独立的，不依赖框架的其他部分，可以单独使用。

### 2. **通用性**
工具应该设计为通用的，能够在多种场景下复用。

### 3. **高性能**
工具组件应该经过性能优化，适合在高并发环境下使用。

### 4. **易用性**
提供简洁明了的 API，支持链式调用等友好的使用方式。

### 5. **标准兼容**
尽可能与行业标准或知名项目（如 Kubernetes）保持兼容。

## 使用指南

### 导入方式

```go
// 导入特定的工具组件
import "github.com/rushteam/beauty/pkg/utils/selector"
import "github.com/rushteam/beauty/pkg/utils/addr"
import "github.com/rushteam/beauty/pkg/utils/bloom"
import "github.com/rushteam/beauty/pkg/utils/uuid"
```

### 最佳实践

1. **选择合适的工具**: 根据具体需求选择最合适的工具组件
2. **复用实例**: 对于可复用的组件（如选择器、布隆过滤器），尽量复用实例
3. **性能测试**: 在生产环境使用前进行性能测试
4. **错误处理**: 妥善处理工具函数返回的错误

### 依赖管理

工具库的各个组件尽量减少外部依赖，只依赖 Go 标准库或必要的第三方库。

## 贡献新工具

欢迎贡献新的通用工具组件！在添加新工具时，请遵循以下准则：

### 1. **目录结构**
```
pkg/utils/your-tool/
├── README.md          # 详细的使用文档
├── your_tool.go       # 主要实现
├── your_tool_test.go  # 单元测试
└── examples/          # 使用示例（可选）
```

### 2. **代码要求**
- 提供完整的文档注释
- 包含充分的单元测试
- 遵循 Go 代码规范
- 处理边界情况和错误

### 3. **文档要求**
- 详细的 README 文档
- API 参考
- 使用示例
- 性能特征说明

### 4. **测试要求**
- 单元测试覆盖率 > 80%
- 包含基准测试
- 测试边界条件

## 版本兼容性

工具库遵循语义化版本控制：

- **主版本**: 不兼容的 API 变更
- **次版本**: 向后兼容的功能增加
- **修订版本**: 向后兼容的问题修复

## 许可证

本工具库采用与主项目相同的许可证。

---

## 快速索引

| 组件 | 功能 | 使用场景 |
|------|------|----------|
| [selector](./selector/) | 标签选择器 | 服务发现、配置管理、资源调度 |
| [addr](./addr/) | 地址工具 | 网络编程、服务注册 |
| [bloom](./bloom/) | 布隆过滤器 | 缓存优化、重复检测 |
| [uuid](./uuid/) | UUID 生成 | 唯一标识生成 |

有问题或建议？欢迎提交 Issue 或 Pull Request！
