# gRPC 标签过滤器

基于 Kubernetes Label Selector 设计的通用标签过滤系统，提供灵活强大的服务实例过滤能力。

## 概述

`LabelFilter` 提供了类似 Kubernetes 标签选择器的功能，支持：
- **精确匹配**：`matchLabels` 键值对匹配
- **表达式匹配**：`matchExpressions` 支持多种操作符
- **便捷方法**：针对常用场景的快捷方法
- **向后兼容**：保持现有地域过滤器 API 兼容

## 支持的操作符

### 基于等式的操作符
- `=` / `FilterOpEquals`：精确等于
- `!=` / `FilterOpNotEquals`：不等于

### 基于集合的操作符
- `in` / `FilterOpIn`：值在指定集合中
- `notin` / `FilterOpNotIn`：值不在指定集合中
- `exists` / `FilterOpExists`：标签存在
- `notexist` / `FilterOpNotExist`：标签不存在

## 基础用法

### 1. 精确匹配过滤

```go
// 使用 WithMatchLabel 添加单个精确匹配
filter := grpcclient.NewLabelFilter().
    WithMatchLabel("region", "us-west-1").
    WithMatchLabel("environment", "production")

client := factory.GetClient("v1alpha.UserService",
    grpcclient.WithDiscoveryLabelFilter(filter),
)

// 或者使用 WithMatchLabels 批量添加
filter2 := grpcclient.NewLabelFilter().
    WithMatchLabels(map[string]string{
        "region":      "us-west-1",
        "environment": "production",
        "status":      "healthy",
    })
```

### 2. 便捷方法（地域过滤）

```go
// 使用便捷方法进行地域过滤
filter := grpcclient.NewLabelFilter().
    WithRegionIn("us-west-1", "us-west-2").
    WithZoneIn("us-west-1a", "us-west-2a").
    WithCampusIn("campus-1").
    WithEnvironmentIn("production", "staging")

client := factory.GetClient("v1alpha.UserService",
    grpcclient.WithDiscoveryLabelFilter(filter),
)
```

### 3. 向后兼容的地域过滤器

```go
// 现有的地域过滤器仍然可用，底层会转换为 FilterLabels
client := factory.GetClient("v1alpha.UserService",
    grpcclient.WithDiscoveryRegionFilter(
        []string{"us-west-1", "us-west-2"}, // regions
        []string{"us-west-1a"},             // zones
        []string{"campus-1"},               // campuses
        []string{"production"},             // environments
    ),
)
```

## 高级用法

### 1. 使用表达式操作符

```go
filter := grpcclient.NewLabelFilter().
    // 服务层级必须是 frontend 或 api
    WithExpression("tier", grpcclient.FilterOpIn, "frontend", "api").
    // 版本不能是 deprecated 或 legacy
    WithExpression("version", grpcclient.FilterOpNotIn, "deprecated", "legacy").
    // 必须有 canary 标签
    WithExpression("canary", grpcclient.FilterOpExists).
    // 不能有 maintenance 标签
    WithExpression("maintenance", grpcclient.FilterOpNotExist)
```

### 2. 复杂过滤场景

```go
// 混合使用多种过滤条件
complexFilter := grpcclient.NewLabelFilter().
    // 精确匹配
    WithMatchLabel("service", "user-service").
    WithMatchLabel("status", "healthy").
    // 地域过滤（便捷方法）
    WithRegionIn("us-west-1", "us-west-2").
    WithEnvironmentIn("production").
    // 高级表达式
    WithExpression("version", grpcclient.FilterOpIn, "v2.0", "v2.1", "v2.2").
    WithExpression("tier", grpcclient.FilterOpNotIn, "deprecated").
    WithExpression("feature-flag", grpcclient.FilterOpExists).
    WithExpression("maintenance", grpcclient.FilterOpNotExist)

client := factory.GetClient("v1alpha.UserService",
    grpcclient.WithDiscoveryLabelFilter(complexFilter),
)
```

### 3. 客户端管理器中使用

```go
// 在客户端管理器中使用标签过滤器
managerFilter := grpcclient.NewLabelFilter().
    WithMatchLabels(map[string]string{
        "service":     "order-service",
        "environment": "production",
    }).
    WithExpression("region", grpcclient.FilterOpIn, "us-west-1", "us-east-1").
    WithExpression("load", grpcclient.FilterOpNotEquals, "high").
    WithExpression("healthy", grpcclient.FilterOpExists)

manager := grpcclient.NewClientManager(discovery, "v1alpha.OrderService",
    grpcclient.WithLoadBalanceStrategy(grpcclient.WeightedRoundRobin),
    grpcclient.WithManagerLabelFilter(managerFilter),
    grpcclient.WithHealthCheck(true, time.Second*30),
    grpcclient.WithFailover(true, 3, time.Second),
)
```

## 过滤器字符串表示

LabelFilter 提供了 `String()` 方法，可以将过滤条件转换为可读的字符串：

```go
filter := grpcclient.NewLabelFilter().
    WithMatchLabel("environment", "production").
    WithExpression("region", grpcclient.FilterOpIn, "us-west-1", "us-east-1").
    WithExpression("tier", grpcclient.FilterOpNotIn, "deprecated").
    WithExpression("canary", grpcclient.FilterOpExists)

fmt.Println(filter.String())
// 输出: environment=production,region in (us-west-1,us-east-1),tier notin (deprecated),canary
```

## 容错机制

当没有服务实例匹配过滤条件时，系统会：
1. 记录警告日志，显示过滤条件
2. 返回所有可用的服务实例（容错机制）
3. 确保服务可用性

## API 对比

### 旧 API (仍然支持)
```go
grpcclient.WithDiscoveryRegionFilter(
    []string{"us-west-1"}, []string{"us-west-1a"}, 
    []string{"campus-1"}, []string{"production"},
)
```

### 新 API
```go
grpcclient.WithDiscoveryLabelFilter(
    grpcclient.NewLabelFilter().
        WithRegionIn("us-west-1").
        WithZoneIn("us-west-1a").
        WithCampusIn("campus-1").
        WithEnvironmentIn("production"),
)
```

### 高级新 API
```go
grpcclient.WithDiscoveryLabelFilter(
    grpcclient.NewLabelFilter().
        WithMatchLabel("region", "us-west-1").
        WithExpression("tier", grpcclient.FilterOpIn, "frontend", "api").
        WithExpression("deprecated", grpcclient.FilterOpNotExist),
)
```

## 最佳实践

1. **优先使用便捷方法**：对于常见的地域过滤，使用 `WithRegionIn` 等便捷方法
2. **合理使用表达式**：复杂条件使用 `WithExpression` 提供更大灵活性
3. **混合使用**：可以同时使用精确匹配和表达式匹配
4. **注意性能**：过滤条件越复杂，性能开销越大
5. **容错设计**：依赖系统的容错机制，确保服务可用性
