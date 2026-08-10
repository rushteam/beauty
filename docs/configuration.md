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

## 密钥与配置分离（Secret Placeholders）

YAML 中写占位符，运行时由 `WithSecrets` 自动解析，避免把敏感信息硬编码在配置文件中。

### 占位符语法

```
${scheme:reference}           # 必须存在，否则报错（strict 模式）
${scheme:reference:-default}  # 解析失败时使用 default 值
```

### 内置 Provider

| scheme | 说明 | 示例 |
|--------|------|------|
| `env` | 从进程环境变量读取 | `${env:DB_PASSWORD}` |
| `file` | 读取文件内容（自动 trim 末尾换行） | `${file:/var/run/secrets/db-pass}` |
| `dotenv` | 从 .env 文件读取，不污染进程环境 | `${dotenv:API_KEY}` |

### 用法

```go
loader, _ := conf.New("config.yaml")
loader = conf.WithSecrets(loader) // 默认启用 env + file Provider
loader.Unmarshal(&cfg)
```

配置文件示例：

```yaml
database:
  password: "${env:DB_PASSWORD}"
tls:
  key: "${file:/var/run/secrets/tls.key}"
api:
  token: "${env:API_TOKEN:-default-token}"
```

### 自定义 Provider

实现 `conf.Provider` 接口即可扩展（如对接 Vault、AWS Secrets Manager）：

```go
loader = conf.WithSecrets(loader,
    conf.EnvProvider(),
    conf.MapProvider("secret", map[string]string{"api": "tok"}),
)
```

### 独立使用

也可以脱离 Loader 单独使用占位符扩展：

```go
// 扩展单个字符串
val, _ := conf.ExpandString(ctx, "pw=${env:DB_PASS}", conf.EnvProvider())

// 扩展整个结构体的 string 字段
conf.Expand(ctx, &cfg, conf.EnvProvider(), conf.FileProvider())
```

## .env 文件支持

通过 [godotenv](https://github.com/joho/godotenv) 支持从 `.env` 文件加载环境变量，适用于本地开发环境。

### 方式一：加载到进程环境

将 `.env` 变量注入进程环境，之后 `${env:VAR}` 占位符和 `os.Getenv` 均可访问。

```go
// 加载 .env（不覆盖已有环境变量）
conf.LoadDotEnv()

// 加载多个文件，按顺序加载
conf.LoadDotEnv(".env", ".env.local")

// 覆盖模式：.env.local 强制覆盖已有变量
conf.OverloadDotEnv(".env", ".env.local")

// 然后正常使用 WithSecrets
loader, _ := conf.New("config.yaml")
loader = conf.WithSecrets(loader) // ${env:VAR} 可解析 .env 中的变量
```

> `LoadDotEnv` 不覆盖已有环境变量（Docker/K8s 注入的优先），`OverloadDotEnv` 强制覆盖（本地调试用）。

### 方式二：DotEnvProvider（不污染进程环境）

直接从 `.env` 文件读取，使用 `${dotenv:VAR}` 语法，不影响 `os.Getenv`：

```go
loader, _ := conf.New("config.yaml")
loader = conf.WithSecrets(loader,
    conf.EnvProvider(),                      // ${env:VAR} — 进程环境
    conf.DotEnvProvider(".env", ".env.local"), // ${dotenv:VAR} — .env 文件
)
```

配置文件示例：

```yaml
database:
  host: "${dotenv:DB_HOST}"
  password: "${env:DB_PASSWORD:-dev123}"
```

### .env 文件格式

```bash
# 注释
DB_HOST=localhost
DB_PORT=5432
DB_PASSWORD="s3cr3t"    # 支持引号
API_KEY='abc-123'       # 单引号
MULTILINE="line1\nline2" # 转义
```

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
