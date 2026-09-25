# gRPC Registry Plugin Mechanism

> 中文版: [grpc-registry-plugin.md](grpc-registry-plugin.md)

## Overview

The Beauty framework provides a flexible registry plugin mechanism that supports dynamically registering and discovering various service registry implementations, avoiding hard-coded dependencies on specific implementations.

## Design Philosophy

### Background

In the previous implementation, the `dial.go` file hard-coded specific registry types:

```go
// hard-coded approach - not recommended
switch u.Scheme {
case "etcd", "nacos":
    return "", nil, nil, fmt.Errorf("scheme %s requires explicit registry via WithRegistry option", u.Scheme)
default:
    return "", nil, nil, fmt.Errorf("unsupported scheme: %s", u.Scheme)
}
```

This approach has the following problems:
- **Poor extensibility**: adding a new registry requires modifying core code
- **High coupling**: client code is coupled to concrete implementations
- **Hard to maintain**: every new implementation requires changes in multiple places

### Solution

The new plugin mechanism is built from the following components:

1. **RegistryFactory**: the registry factory interface
2. **RegistryManager**: the global registry manager
3. **Auto-registration**: each implementation package registers itself via its `init()` function

## Core Components

### RegistryFactory Interface

```go
type RegistryFactory interface {
    // Scheme returns the supported protocol scheme
    Scheme() string
    
    // CreateFromURL creates a registry instance from a URL
    CreateFromURL(targetURL *url.URL) (Discovery, error)
    
    // CreateFromConfig creates a registry instance from a config
    CreateFromConfig(config interface{}) (Discovery, error)
}
```

### RegistryManager

```go
type RegistryManager struct {
    factories map[string]RegistryFactory
    mu        sync.RWMutex
}
```

It provides the following functionality:
- **Register a factory**: `RegisterFactory(factory RegistryFactory)`
- **Create a registry**: `CreateRegistry(target string) (Discovery, error)`
- **List supported schemes**: `GetAvailableSchemes() []string`
- **Check scheme support**: `IsSchemeSupported(scheme string) bool`

## Usage

### 1. Auto-registration (recommended)

Each registry implementation package registers itself automatically via its `init()` function:

```go
// pkg/service/discover/etcdv3/factory.go
func init() {
    discover.RegisterFactoryFunc("etcd", createRegistryFromURL)
    discover.RegisterFactoryFunc("etcdv3", createRegistryFromURL) // alias
}
```

### 2. Manual registration

```go
// register a custom factory
discover.RegisterFactoryFunc("myregistry", func(targetURL *url.URL) (discover.Discovery, error) {
    // create the custom registry
    return myRegistry, nil
})
```

### 3. Using a registry

```go
// create via the manager
manager := discover.GetManager()
registry, err := manager.CreateRegistry("etcd://127.0.0.1:2379")

// create automatically via DialContext
conn, err := grpcclient.DialContext(ctx, "etcd://127.0.0.1:2379/v1alpha.UserService")
```

## Supported Registries

| Scheme | Implementation package | Alias | Status |
|------|--------|------|------|
| `etcd` | `pkg/service/discover/etcdv3` | `etcdv3` | ✅ Supported |
| `nacos` | `pkg/service/discover/nacos` | - | ✅ Supported |
| `polaris` | `pkg/service/discover/polaris` | - | ✅ Supported |
| `k8s` | `pkg/service/discover/k8s` | `kubernetes` | ✅ Supported |

### K8s Service Discovery DSN

K8s service discovery URLs use the **K8s DNS style**:

```text
k8s://service.namespace[.svc[.cluster.local]]?params
```

Examples:

```go
// exact service discovery
conn, _ := grpc.Dial("k8s://payment-internal.mall?port_name=grpc")

// namespace omitted (defaults to default)
conn, _ := grpc.Dial("k8s://my-service?port_name=grpc")

// full DNS form
conn, _ := grpc.Dial("k8s://my-svc.kube-system.svc.cluster.local?port_name=http")

// wildcard: select multiple Services in the namespace by label
conn, _ := grpc.Dial("k8s://*.mall?label_selector=team=payment")
```

Available query parameters: `namespace`, `service_type` (defaults to ClusterIP), `port_name`, `label_selector`, `kubeconfig`, `watch_timeout`.

> For details, see [pkg/service/discover/k8s/README.md](../pkg/service/discover/k8s/README.md).

## Adding a New Registry

### 1. Implement the registry

```go
// pkg/service/discover/myregistry/registry.go
type Registry struct {
    // implements the discover.Discovery interface
}

func (r *Registry) Find(ctx context.Context, serviceName string) ([]discover.ServiceInfo, error) {
    // implement the service discovery logic
}

func (r *Registry) Watch(ctx context.Context, serviceName string, notify discover.Notify) error {
    // implement the service watch logic
}
```

### 2. Implement the factory

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

### 3. Implement config parsing

```go
func parseConfigFromURL(targetURL *url.URL) (*Config, error) {
    config := &Config{
        Endpoints: []string{targetURL.Host},
    }
    
    // parse query parameters
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

## Advanced Usage

### 1. Dynamic registration

```go
// dynamically register a new registry at runtime
discover.RegisterFactoryFunc("dynamic", func(targetURL *url.URL) (discover.Discovery, error) {
    // create the registry dynamically based on the URL
    return createDynamicRegistry(targetURL), nil
})
```

### 2. Batch registration

```go
// register multiple schemes in bulk
schemes := []string{"etcd", "nacos", "polaris"}
for _, scheme := range schemes {
    discover.RegisterFactoryFunc(scheme, createRegistryFromURL)
}
```

### 3. Conditional registration

```go
func init() {
    // register only under specific conditions
    if os.Getenv("ENABLE_CUSTOM_REGISTRY") == "true" {
        discover.RegisterFactoryFunc("custom", createCustomRegistry)
    }
}
```

## Error Handling

### Common errors

1. **Unsupported scheme**
   ```
   unsupported registry scheme: unknown, available schemes: [etcd nacos polaris k8s]
   ```

2. **Invalid URL**
   ```
   invalid target URL invalid-url: parse "invalid-url": invalid URI for request
   ```

3. **Creation failure**
   ```
   failed to create registry for scheme etcd: connection refused
   ```

### Error handling best practices

```go
registry, err := manager.CreateRegistry(target)
if err != nil {
    // check whether the scheme is unsupported
    if strings.Contains(err.Error(), "unsupported registry scheme") {
        log.Printf("please use a supported registry scheme: %v", manager.GetAvailableSchemes())
        return
    }
    
    // other errors
    log.Printf("failed to create registry: %v", err)
    return
}
```

## Performance Considerations

### 1. Factory caching

The manager caches all registered factories internally, avoiding repeated lookups:

```go
m.mu.RLock()
factory, exists := m.factories[scheme]
m.mu.RUnlock()
```

### 2. Registry instantiation

Every call to `CreateRegistry` creates a new registry instance; caching it at the application level is recommended:

```go
// application-level cache
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

## Testing

### Unit tests

```go
func TestRegistryManager(t *testing.T) {
    manager := discover.GetManager()
    
    // test registration
    manager.RegisterFactoryFunc("test", func(targetURL *url.URL) (discover.Discovery, error) {
        return &mockRegistry{}, nil
    })
    
    // test creation
    registry, err := manager.CreateRegistry("test://example.com")
    assert.NoError(t, err)
    assert.NotNil(t, registry)
    
    // test scheme listing
    schemes := manager.GetAvailableSchemes()
    assert.Contains(t, schemes, "test")
}
```

### Integration tests

```go
func TestDialContextWithPlugin(t *testing.T) {
    // register a test registry
    discover.RegisterFactoryFunc("test", createTestRegistry)
    
    // test the connection
    conn, err := grpcclient.DialContext(context.Background(), "test://example.com/service")
    assert.NoError(t, err)
    assert.NotNil(t, conn)
    
    conn.Close()
}
```

## Migration Guide

### Migrating from hard-coding

**Before**:
```go
switch u.Scheme {
case "etcd", "nacos":
    return "", nil, nil, fmt.Errorf("scheme %s requires explicit registry via WithRegistry option", u.Scheme)
}
```

**After**:
```go
registry, err = createRegistryFromScheme(u.Scheme, u)
if err != nil {
    return "", nil, nil, fmt.Errorf("failed to create registry for scheme %s: %w", u.Scheme, err)
}
```

### Compatibility

- ✅ **Backward compatible**: the existing `WithRegistry()` option still works
- ✅ **URL format compatible**: existing URL formats need no changes
- ✅ **API compatible**: existing API call patterns need no changes

## Summary

The new plugin mechanism provides:

1. **Better extensibility**: adding a new registry requires no changes to core code
2. **Lower coupling**: client code is decoupled from concrete implementations
3. **Easier maintenance**: each implementation is managed independently
4. **More flexibility**: supports dynamic registration at runtime
5. **More robustness**: unified error handling and type safety

This design follows the open-closed principle (open for extension, closed for modification), making the framework more flexible and maintainable.
