# 标签选择器 (Label Selector)

基于 Kubernetes Label Selector 设计的通用标签过滤组件，提供灵活强大的标签匹配和过滤能力。

## 概述

此组件完全参考 Kubernetes 的标签选择器设计，提供与 K8s 一致的 API 和语义，可以在任何需要基于标签进行过滤的场景中使用。

## 特性

- ✅ **Kubernetes 兼容**: API 设计完全对齐 K8s LabelSelector
- ✅ **类型安全**: 强类型的操作符和参数验证
- ✅ **高性能**: 优化的匹配算法
- ✅ **易于使用**: 流式 API 设计，支持方法链调用
- ✅ **通用性强**: 可用于任何带标签的对象过滤
- ✅ **便捷方法**: 针对常用场景提供快捷方法

## 支持的操作符

### 基于等式的操作符
- `FilterOpEquals` (`=`): 精确等于
- `FilterOpNotEquals` (`!=`): 不等于

### 基于集合的操作符
- `FilterOpIn` (`in`): 值在指定集合中
- `FilterOpNotIn` (`notin`): 值不在指定集合中
- `FilterOpExists` (`exists`): 标签存在
- `FilterOpNotExist` (`notexist`): 标签不存在

## 快速开始

### 基本用法

```go
package main

import (
    "fmt"
    "github.com/rushteam/beauty/pkg/utils/selector"
)

func main() {
    // 创建标签选择器
    filter := selector.NewLabelFilter().
        WithMatchLabel("environment", "production").
        WithMatchLabel("tier", "frontend")

    // 测试标签
    labels := map[string]string{
        "environment": "production",
        "tier":        "frontend",
        "version":     "v1.0",
    }

    // 检查匹配
    if filter.Matches(labels) {
        fmt.Println("标签匹配!")
    }
}
```

### 高级用法

```go
// 复杂的标签选择器
filter := selector.NewLabelFilter().
    // 精确匹配
    WithMatchLabel("app", "my-service").
    WithMatchLabel("environment", "production").
    
    // 集合操作
    WithExpression("tier", selector.FilterOpIn, "frontend", "api").
    WithExpression("version", selector.FilterOpNotIn, "deprecated", "beta").
    
    // 存在性检查
    WithExpression("monitoring", selector.FilterOpExists).
    WithExpression("legacy", selector.FilterOpNotExist)

// 检查单个对象
if filter.Matches(objectLabels) {
    // 处理匹配的对象
}

// 批量过滤（需要自定义标签提取函数）
filtered := filter.FilterMap(objects, func(obj interface{}) map[string]string {
    // 返回对象的标签
    return obj.(MyObject).Labels
})
```

### 便捷方法

```go
// 针对常见的地域/环境过滤场景
filter := selector.NewLabelFilter().
    WithRegionIn("us-west-1", "us-west-2").        // 地域过滤
    WithZoneIn("us-west-1a", "us-west-2a").        // 可用区过滤
    WithCampusIn("campus-1").                      // 园区过滤
    WithEnvironmentIn("production", "staging")      // 环境过滤
```

## API 参考

### 核心类型

#### `LabelSelector`
```go
type LabelSelector struct {
    MatchLabels      map[string]string               `json:"matchLabels,omitempty"`
    MatchExpressions []LabelSelectorRequirement      `json:"matchExpressions,omitempty"`
}
```

#### `LabelSelectorRequirement`
```go
type LabelSelectorRequirement struct {
    Key      string         `json:"key"`
    Operator FilterOperator `json:"operator"`
    Values   []string       `json:"values"`
}
```

#### `LabelFilter`
```go
type LabelFilter struct {
    selector *LabelSelector
}
```

### 主要方法

#### 创建和配置

- `NewLabelFilter() *LabelFilter`: 创建新的标签过滤器
- `WithMatchLabel(key, value string) *LabelFilter`: 添加精确匹配
- `WithMatchLabels(labels map[string]string) *LabelFilter`: 批量添加精确匹配
- `WithExpression(key string, op FilterOperator, values ...string) *LabelFilter`: 添加表达式

#### 便捷方法

- `WithRegionIn(regions ...string) *LabelFilter`: 地域过滤
- `WithZoneIn(zones ...string) *LabelFilter`: 可用区过滤
- `WithCampusIn(campuses ...string) *LabelFilter`: 园区过滤
- `WithEnvironmentIn(environments ...string) *LabelFilter`: 环境过滤

#### 匹配和过滤

- `Matches(labels map[string]string) bool`: 检查标签是否匹配
- `FilterMap(items interface{}, getLabelsFn func(interface{}) map[string]string) []interface{}`: 过滤对象列表
- `String() string`: 返回选择器的字符串表示

## 使用场景

### 1. 服务发现过滤

```go
// 过滤服务实例
filter := selector.NewLabelFilter().
    WithMatchLabel("service", "user-api").
    WithExpression("region", selector.FilterOpIn, "us-west-1", "us-east-1").
    WithExpression("healthy", selector.FilterOpExists)

// 应用到服务实例列表
matchedServices := filter.FilterMap(services, func(svc interface{}) map[string]string {
    return svc.(ServiceInstance).Metadata
})
```

### 2. 配置管理

```go
// 根据环境和应用过滤配置
filter := selector.NewLabelFilter().
    WithMatchLabel("app", "my-app").
    WithEnvironmentIn("production", "staging")

configs := getConfigs()
relevantConfigs := filter.FilterMap(configs, func(cfg interface{}) map[string]string {
    return cfg.(Config).Labels
})
```

### 3. 资源调度

```go
// 选择合适的节点
filter := selector.NewLabelFilter().
    WithExpression("node-type", selector.FilterOpIn, "compute", "gpu").
    WithExpression("maintenance", selector.FilterOpNotExist).
    WithMatchLabel("zone", "us-west-1a")

availableNodes := filter.FilterMap(nodes, func(node interface{}) map[string]string {
    return node.(Node).Labels
})
```

## 字符串表示

选择器可以转换为可读的字符串格式：

```go
filter := selector.NewLabelFilter().
    WithMatchLabel("environment", "production").
    WithExpression("region", selector.FilterOpIn, "us-west-1", "us-east-1").
    WithExpression("tier", selector.FilterOpNotIn, "deprecated")

fmt.Println(filter.String())
// 输出: environment=production,region in (us-west-1,us-east-1),tier notin (deprecated)
```

## 性能考虑

1. **标签数量**: 标签数量对匹配性能影响较小
2. **表达式复杂度**: 复杂的表达式（特别是大的 `in`/`notin` 集合）会影响性能
3. **对象数量**: 使用 `FilterMap` 时，对象数量线性影响性能
4. **标签提取**: 自定义的标签提取函数应尽可能高效

## 最佳实践

1. **优先使用精确匹配**: `WithMatchLabel` 比表达式更高效
2. **合理使用便捷方法**: 对于常见场景，使用 `WithRegionIn` 等便捷方法
3. **避免过度复杂**: 保持选择器逻辑简单明了
4. **复用选择器**: 对于相同的过滤条件，可以复用同一个选择器实例
5. **性能测试**: 对于高频使用的场景，进行性能测试和优化

## 与 Kubernetes 的对比

| 功能 | Kubernetes | 此组件 | 兼容性 |
|------|------------|--------|--------|
| MatchLabels | ✅ | ✅ | 完全兼容 |
| MatchExpressions | ✅ | ✅ | 完全兼容 |
| In 操作符 | ✅ | ✅ | 完全兼容 |
| NotIn 操作符 | ✅ | ✅ | 完全兼容 |
| Exists 操作符 | ✅ | ✅ | 完全兼容 |
| DoesNotExist 操作符 | ✅ | ✅ | 完全兼容 |
| 便捷方法 | ❌ | ✅ | 扩展功能 |
| 通用过滤 | ❌ | ✅ | 扩展功能 |

## 贡献

欢迎贡献代码和提出建议！请确保：

1. 遵循现有的代码风格
2. 添加适当的测试
3. 更新相关文档
4. 保持与 Kubernetes 的兼容性

## 许可证

本项目采用与主项目相同的许可证。
