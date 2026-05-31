# 配置系统

Beauty 的配置系统通过统一的 `conf.Loader` 接口屏蔽底层差异，本地文件和远程配置中心使用完全相同的 API。

## 快速开始

```go
import "github.com/rushteam/beauty/pkg/conf"

// 本地文件（无 scheme 或 file://）
loader, err := conf.New("config/app.yaml")

// 反序列化到结构体
var cfg AppConfig
if err := loader.Unmarshal(&cfg); err != nil {
    log.Fatal(err)
}

// 监听变更（热加载）
loader.Watch(ctx, func() {
    var newCfg AppConfig
    loader.Unmarshal(&newCfg)
    // 应用新配置…
})
```

## 本地文件

支持 YAML、JSON、TOML 等格式，依赖 fsnotify 实现文件变更监听。

```go
// 以下三种写法等价
loader, _ := conf.New("config/app.yaml")
loader, _ := conf.New("./config/app.yaml")
loader, _ := conf.New("file:///abs/path/config.yaml")
```

文件格式由扩展名自动推断，无需额外配置。

## 远程配置中心

通过 URL scheme 区分不同的配置中心，**使用前需 import 对应的 infra 包**触发工厂注册。

### etcd

```go
import _ "github.com/rushteam/beauty/pkg/infra/etcd"

// 基本用法
loader, _ := conf.New("etcd://127.0.0.1:2379/myapp/config.yaml")

// 多节点 + 认证
loader, _ := conf.New("etcd://user:pass@node1:2379,node2:2379/myapp/config.yaml?dial_ms=3000")
```

| 参数 | 说明 | 默认值 |
|------|------|--------|
| Host | 节点地址，多个用逗号分隔 | — |
| User/Password | etcd 认证 | — |
| Path | 配置 key（去掉前导 `/`） | — |
| `dial_ms` | 连接超时（毫秒） | 3000 |

etcd Watch 支持前缀监听：key 以 `/` 结尾时自动加 `WithPrefix()`。

### Nacos

```go
import _ "github.com/rushteam/beauty/pkg/infra/nacos"

loader, _ := conf.New("nacos://127.0.0.1:8848/myapp.yaml?namespace=dev&group=DEFAULT_GROUP")

// 多节点
loader, _ := conf.New("nacos://n1:8848,n2:8848/myapp.yaml?namespace=prod")
```

| 参数 | 说明 | 默认值 |
|------|------|--------|
| Host | 节点地址，多个用逗号分隔 | — |
| User/Password | Nacos 认证 | — |
| Path | DataID（去掉前导 `/`） | — |
| `namespace` | 命名空间 | — |
| `group` | 配置分组 | `DEFAULT_GROUP` |
| `app_name` | 应用名 | — |

### Consul

```go
import _ "github.com/rushteam/beauty/pkg/infra/consul"

// token 通过 URL password 传入
loader, _ := conf.New("consul://:mytoken@127.0.0.1:8500/myapp/config.yaml")

// 指定 datacenter / namespace
loader, _ := conf.New("consul://127.0.0.1:8500/myapp/config.yaml?datacenter=dc1&namespace=ns1")
```

key 对应 Consul KV 中的完整路径（URL Path 去掉前导 `/`）。

| 参数 | 说明 | 默认值 |
|------|------|--------|
| Host | Consul 地址 | — |
| Password | ACL Token | — |
| Path | KV 路径 | — |
| `datacenter` | 数据中心 | — |
| `namespace` | 命名空间（企业版） | — |
| `partition` | 分区（企业版） | — |

### Polaris

```go
import _ "github.com/rushteam/beauty/pkg/infra/polaris"

// key 格式：fileGroup/fileName
loader, _ := conf.New("polaris://127.0.0.1:8091/DEFAULT_GROUP/app.yaml?namespace=default")
```

| 参数 | 说明 | 默认值 |
|------|------|--------|
| Host | Polaris 地址，多个用逗号分隔 | — |
| Password | 访问 Token | — |
| Path | `fileGroup/fileName` | — |
| `namespace` | 命名空间 | `default` |

## 格式推断与覆盖

配置格式从 key/path 的扩展名自动推断，不带扩展名时默认 `yaml`。

也可以通过 query 参数强制指定：

```go
loader, _ := conf.New("etcd://127.0.0.1:2379/myapp/config?format=json")
```

## 热加载

所有 Loader（文件和远程）均支持热加载。`Watch` 注册的回调在配置变更时异步触发，ctx 取消后自动停止监听。

```go
loader.Watch(ctx, func() {
    var cfg AppConfig
    if err := loader.Unmarshal(&cfg); err != nil {
        logger.Error("reload config failed", "err", err)
        return
    }
    applyConfig(cfg)
})
```

> `Unmarshal` 每次调用都从最新内容解析，热加载后直接调用即可拿到新值，无需额外同步。

## 扩展：注册自定义配置中心

实现 `conf.ConfigCenter` 接口，在 `init()` 中注册即可：

```go
package mycc

import (
    "context"
    "net/url"
    "github.com/rushteam/beauty/pkg/conf"
)

func init() {
    conf.RegisterFactory("mycc", func(u *url.URL) (conf.ConfigCenter, error) {
        return &myConfigCenter{addr: u.Host}, nil
    })
}

type myConfigCenter struct{ addr string }

func (c *myConfigCenter) Get(ctx context.Context, key string) (string, error) { … }
func (c *myConfigCenter) Watch(ctx context.Context, key string, onChange func(key, value string)) (context.CancelFunc, error) { … }
```

用法：

```go
import _ "your-project/mycc"
loader, _ := conf.New("mycc://host/key")
```
