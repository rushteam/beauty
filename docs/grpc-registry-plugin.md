# gRPC 注册中心插件机制

## 概述

Beauty 框架提供了灵活的注册中心插件机制，支持动态注册和发现各种服务注册中心实现，避免了硬编码特定实现的问题。

## 设计理念

### 问题背景

之前的实现中，`dial.go` 文件硬编码了特定的注册中心类型：

```go
// 硬编码的方式 - 不推荐
switch u.Scheme {
case "etcd", "nacos":
    return "", nil, nil, fmt.Errorf("scheme %s requires explicit registry via WithRegistry option", u.Scheme)
default:
    return "", nil, nil, fmt.Errorf("unsupported scheme: %s", u.Scheme)
}
```

这种方式存在以下问题：
- **扩展性差**: 添加新的注册中心需要修改核心代码
- **耦合度高**: 客户端代码与具体实现耦合
- **维护困难**: 每次添加新实现都需要修改多个地方

### 解决方案

新的插件机制通过以下组件实现：

1. **RegistryFactory**: 注册中心工厂接口
2. **RegistryManager**: 全局注册中心管理器
3. **自动注册**: 各实现包通过 `init()` 函数自动注册

## 核心组件

### RegistryFactory 接口

```go
type RegistryFactory interface {
    // Scheme 返回支持的协议方案
    Scheme() string
    
    // CreateFromURL 从URL创建注册中心实例
    CreateFromURL(targetURL *url.URL) (Discovery, error)
    
    // CreateFromConfig 从配置创建注册中心实例
    CreateFromConfig(config interface{}) (Discovery, error)
}
```

### RegistryManager 管理器

```go
type RegistryManager struct {
    factories map[string]RegistryFactory
    mu        sync.RWMutex
}
```

提供以下功能：
- **注册工厂**: `RegisterFactory(factory RegistryFactory)`
- **创建注册中心**: `CreateRegistry(target string) (Discovery, error)`
- **查询支持方案**: `GetAvailableSchemes() []string`
- **检查方案支持**: `IsSchemeSupported(scheme string) bool`

## 使用方式

### 1. 自动注册（推荐）

各注册中心实现包通过 `init()` 函数自动注册：

```go
// pkg/service/discover/etcdv3/factory.go
func init() {
    discover.RegisterFactoryFunc("etcd", createRegistryFromURL)
    discover.RegisterFactoryFunc("etcdv3", createRegistryFromURL) // 别名
}
```

### 2. 手动注册

```go
// 注册自定义工厂
discover.RegisterFactoryFunc("myregistry", func(targetURL *url.URL) (discover.Discovery, error) {
    // 创建自定义注册中心
    return myRegistry, nil
})
```

### 3. 使用注册中心

```go
// 通过管理器创建
manager := discover.GetManager()
registry, err := manager.CreateRegistry("etcd://127.0.0.1:2379")

// 通过 DialContext 自动创建
conn, err := grpcclient.DialContext(ctx, "etcd://127.0.0.1:2379/v1alpha.UserService")
```

## 支持的注册中心

| 方案 | 实现包 | 别名 | 状态 |
|------|--------|------|------|
| `etcd` | `pkg/service/discover/etcdv3` | `etcdv3` | ✅ 支持 |
| `nacos` | `pkg/service/discover/nacos` | - | ✅ 支持 |
| `polaris` | `pkg/service/discover/polaris` | - | ✅ 支持 |
| `k8s` | `pkg/service/discover/k8s` | `kubernetes` | ✅ 支持 |

## 扩展新的注册中心

### 1. 实现注册中心

```go
// pkg/service/discover/myregistry/registry.go
type Registry struct {
    // 实现 discover.Discovery 接口
}

func (r *Registry) Find(ctx context.Context, serviceName string) ([]discover.ServiceInfo, error) {
    // 实现服务发现逻辑
}

func (r *Registry) Watch(ctx context.Context, serviceName string, notify discover.Notify) error {
    // 实现服务监听逻辑
}
```

### 2. 实现工厂

```go
// pkg/service/discover/myregistry/factory.go
func init() {
    discover.RegisterFactoryFunc("myregistry", createRegistryFromURL)
}

func createRegistryFromURL(targetURL *url.URL) (discover.Discovery, error) {
    config, err := parseConfigFromURL(targetURL)
    if err != nil {
        return nil, err
    }
    
    registry := NewRegistry(config)
    return registry, nil
}
```

### 3. 实现配置解析

```go
func parseConfigFromURL(targetURL *url.URL) (*Config, error) {
    config := &Config{
        Endpoints: []string{targetURL.Host},
    }
    
    // 解析查询参数
    for k, v := range targetURL.Query() {
        switch k {
        case "namespace":
            config.Namespace = v[0]
        case "timeout":
            if timeout, err := time.ParseDuration(v[0]); err == nil {
                config.Timeout = timeout
            }
        }
    }
    
    return config, nil
}
```

## 高级用法

### 1. 动态注册

```go
// 运行时动态注册新的注册中心
discover.RegisterFactoryFunc("dynamic", func(targetURL *url.URL) (discover.Discovery, error) {
    // 根据URL动态创建注册中心
    return createDynamicRegistry(targetURL), nil
})
```

### 2. 批量注册

```go
// 批量注册多个方案
schemes := []string{"etcd", "nacos", "polaris"}
for _, scheme := range schemes {
    discover.RegisterFactoryFunc(scheme, createRegistryFromURL)
}
```

### 3. 条件注册

```go
func init() {
    // 只在特定条件下注册
    if os.Getenv("ENABLE_CUSTOM_REGISTRY") == "true" {
        discover.RegisterFactoryFunc("custom", createCustomRegistry)
    }
}
```

## 错误处理

### 常见错误

1. **不支持的方案**
   ```
   unsupported registry scheme: unknown, available schemes: [etcd nacos polaris k8s]
   ```

2. **无效的URL**
   ```
   invalid target URL invalid-url: parse "invalid-url": invalid URI for request
   ```

3. **创建失败**
   ```
   failed to create registry for scheme etcd: connection refused
   ```

### 错误处理最佳实践

```go
registry, err := manager.CreateRegistry(target)
if err != nil {
    // 检查是否是不支持的方案
    if strings.Contains(err.Error(), "unsupported registry scheme") {
        log.Printf("请使用支持的注册中心方案: %v", manager.GetAvailableSchemes())
        return
    }
    
    // 其他错误
    log.Printf("创建注册中心失败: %v", err)
    return
}
```

## 性能考虑

### 1. 工厂缓存

管理器内部缓存了所有注册的工厂，避免重复查找：

```go
m.mu.RLock()
factory, exists := m.factories[scheme]
m.mu.RUnlock()
```

### 2. 注册中心实例化

每次调用 `CreateRegistry` 都会创建新的注册中心实例，建议在应用级别缓存：

```go
// 应用级别缓存
var registryCache = make(map[string]discover.Discovery)

func getRegistry(target string) (discover.Discovery, error) {
    if cached, exists := registryCache[target]; exists {
        return cached, nil
    }
    
    registry, err := manager.CreateRegistry(target)
    if err != nil {
        return nil, err
    }
    
    registryCache[target] = registry
    return registry, nil
}
```

## 测试

### 单元测试

```go
func TestRegistryManager(t *testing.T) {
    manager := discover.GetManager()
    
    // 测试注册
    manager.RegisterFactoryFunc("test", func(targetURL *url.URL) (discover.Discovery, error) {
        return &mockRegistry{}, nil
    })
    
    // 测试创建
    registry, err := manager.CreateRegistry("test://example.com")
    assert.NoError(t, err)
    assert.NotNil(t, registry)
    
    // 测试方案查询
    schemes := manager.GetAvailableSchemes()
    assert.Contains(t, schemes, "test")
}
```

### 集成测试

```go
func TestDialContextWithPlugin(t *testing.T) {
    // 注册测试注册中心
    discover.RegisterFactoryFunc("test", createTestRegistry)
    
    // 测试连接
    conn, err := grpcclient.DialContext(context.Background(), "test://example.com/service")
    assert.NoError(t, err)
    assert.NotNil(t, conn)
    
    conn.Close()
}
```

## 迁移指南

### 从硬编码迁移

**迁移前**:
```go
switch u.Scheme {
case "etcd", "nacos":
    return "", nil, nil, fmt.Errorf("scheme %s requires explicit registry via WithRegistry option", u.Scheme)
}
```

**迁移后**:
```go
registry, err = createRegistryFromScheme(u.Scheme, u)
if err != nil {
    return "", nil, nil, fmt.Errorf("failed to create registry for scheme %s: %w", u.Scheme, err)
}
```

### 兼容性

- ✅ **向后兼容**: 现有的 `WithRegistry()` 选项仍然有效
- ✅ **URL格式兼容**: 现有的URL格式无需修改
- ✅ **API兼容**: 现有的API调用方式无需修改

## 总结

新的插件机制提供了：

1. **更好的扩展性**: 添加新注册中心无需修改核心代码
2. **更低的耦合**: 客户端代码与具体实现解耦
3. **更易维护**: 每个实现独立管理
4. **更灵活**: 支持运行时动态注册
5. **更健壮**: 统一的错误处理和类型安全

这种设计遵循了开闭原则（对扩展开放，对修改关闭），使得框架更加灵活和可维护。
