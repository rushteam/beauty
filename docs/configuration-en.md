# Configuration System

Beauty's configuration system hides backend differences behind a unified `conf.Loader` interface. Local files and remote configuration centers use exactly the same API.

## Quick Start

```go
import "github.com/rushteam/beauty/pkg/conf"

// Local file (no scheme or file://)
loader, err := conf.New("config/app.yaml")

// Unmarshal into a struct
var cfg AppConfig
if err := loader.Unmarshal(&cfg); err != nil {
    log.Fatal(err)
}

// Watch for changes (hot reload)
loader.Watch(ctx, func() {
    var newCfg AppConfig
    loader.Unmarshal(&newCfg)
    // Apply new configuration…
})
```

## Local Files

Supports YAML, JSON, TOML, and other formats. File change watching is powered by fsnotify.

```go
// The following three forms are equivalent
loader, _ := conf.New("config/app.yaml")
loader, _ := conf.New("./config/app.yaml")
loader, _ := conf.New("file:///abs/path/config.yaml")
```

File format is inferred automatically from the extension; no extra configuration is required.

## Remote Configuration Centers

Different configuration centers are distinguished by URL scheme. **Import the corresponding infra package before use** to trigger factory registration.

### etcd

```go
import _ "github.com/rushteam/beauty/pkg/infra/etcd"

// Basic usage
loader, _ := conf.New("etcd://127.0.0.1:2379/myapp/config.yaml")

// Multiple nodes + authentication
loader, _ := conf.New("etcd://user:pass@node1:2379,node2:2379/myapp/config.yaml?dial_ms=3000")
```

| Parameter | Description | Default |
|------|------|--------|
| Host | Node addresses, comma-separated for multiple | — |
| User/Password | etcd authentication | — |
| Path | Configuration key (leading `/` stripped) | — |
| `dial_ms` | Connection timeout (milliseconds) | 3000 |

etcd Watch supports prefix listening: when the key ends with `/`, `WithPrefix()` is applied automatically.

### Nacos

```go
import _ "github.com/rushteam/beauty/pkg/infra/nacos"

loader, _ := conf.New("nacos://127.0.0.1:8848/myapp.yaml?namespace=dev&group=DEFAULT_GROUP")

// Multiple nodes
loader, _ := conf.New("nacos://n1:8848,n2:8848/myapp.yaml?namespace=prod")
```

| Parameter | Description | Default |
|------|------|--------|
| Host | Node addresses, comma-separated for multiple | — |
| User/Password | Nacos authentication | — |
| Path | DataID (leading `/` stripped) | — |
| `namespace` | Namespace | — |
| `group` | Configuration group | `DEFAULT_GROUP` |
| `app_name` | Application name | — |

### Consul

```go
import _ "github.com/rushteam/beauty/pkg/infra/consul"

// Pass token via URL password
loader, _ := conf.New("consul://:mytoken@127.0.0.1:8500/myapp/config.yaml")

// Specify datacenter / namespace
loader, _ := conf.New("consul://127.0.0.1:8500/myapp/config.yaml?datacenter=dc1&namespace=ns1")
```

The key corresponds to the full path in Consul KV (URL Path with leading `/` stripped).

| Parameter | Description | Default |
|------|------|--------|
| Host | Consul address | — |
| Password | ACL Token | — |
| Path | KV path | — |
| `datacenter` | Datacenter | — |
| `namespace` | Namespace (Enterprise) | — |
| `partition` | Partition (Enterprise) | — |

### Polaris

```go
import _ "github.com/rushteam/beauty/pkg/infra/polaris"

// Key format: fileGroup/fileName
loader, _ := conf.New("polaris://127.0.0.1:8091/DEFAULT_GROUP/app.yaml?namespace=default")
```

| Parameter | Description | Default |
|------|------|--------|
| Host | Polaris address, comma-separated for multiple | — |
| Password | Access Token | — |
| Path | `fileGroup/fileName` | — |
| `namespace` | Namespace | `default` |

## Format Inference and Override

Configuration format is inferred automatically from the key/path extension. When no extension is present, `yaml` is the default.

You can also force a format via query parameter:

```go
loader, _ := conf.New("etcd://127.0.0.1:2379/myapp/config?format=json")
```

## Hot Reload

All Loaders (file and remote) support hot reload. Callbacks registered with `Watch` are triggered asynchronously on configuration changes; listening stops automatically when ctx is cancelled.

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

> Each `Unmarshal` call parses from the latest content. After hot reload, call it directly to get the new values—no extra synchronization is needed.

## Secrets and Configuration Separation (Secret Placeholders)

Write placeholders in YAML; at runtime `WithSecrets` resolves them automatically, avoiding hard-coded sensitive values in configuration files.

### Placeholder Syntax

```
${scheme:reference}           # Must exist, otherwise error (strict mode)
${scheme:reference:-default}  # Use default value when resolution fails
```

### Built-in Providers

| scheme | Description | Example |
|--------|------|------|
| `env` | Read from process environment variables | `${env:DB_PASSWORD}` |
| `file` | Read file contents (trims trailing newline automatically) | `${file:/var/run/secrets/db-pass}` |
| `dotenv` | Read from .env file without polluting process environment | `${dotenv:API_KEY}` |

### Usage

```go
loader, _ := conf.New("config.yaml")
loader = conf.WithSecrets(loader) // Enables env + file Provider by default
loader.Unmarshal(&cfg)
```

Configuration file example:

```yaml
database:
  password: "${env:DB_PASSWORD}"
tls:
  key: "${file:/var/run/secrets/tls.key}"
api:
  token: "${env:API_TOKEN:-default-token}"
```

### Custom Provider

Implement the `conf.Provider` interface to extend (e.g. integrate with Vault, AWS Secrets Manager):

```go
loader = conf.WithSecrets(loader,
    conf.EnvProvider(),
    conf.MapProvider("secret", map[string]string{"api": "tok"}),
)
```

### Standalone Usage

Placeholder expansion can also be used independently of Loader:

```go
// Expand a single string
val, _ := conf.ExpandString(ctx, "pw=${env:DB_PASS}", conf.EnvProvider())

// Expand all string fields in a struct
conf.Expand(ctx, &cfg, conf.EnvProvider(), conf.FileProvider())
```

## .env File Support

Via [godotenv](https://github.com/joho/godotenv), environment variables can be loaded from `.env` files—ideal for local development.

### Option 1: Load into Process Environment

Inject `.env` variables into the process environment; afterward both `${env:VAR}` placeholders and `os.Getenv` can access them.

```go
// Load .env (does not override existing environment variables)
conf.LoadDotEnv()

// Load multiple files in order
conf.LoadDotEnv(".env", ".env.local")

// Overload mode: .env.local forcibly overrides existing variables
conf.OverloadDotEnv(".env", ".env.local")

// Then use WithSecrets as usual
loader, _ := conf.New("config.yaml")
loader = conf.WithSecrets(loader) // ${env:VAR} can resolve variables from .env
```

> `LoadDotEnv` does not override existing environment variables (Docker/K8s-injected values take precedence); `OverloadDotEnv` forcibly overrides (for local debugging).

### Option 2: DotEnvProvider (Without Polluting Process Environment)

Read directly from `.env` files using `${dotenv:VAR}` syntax, without affecting `os.Getenv`:

```go
loader, _ := conf.New("config.yaml")
loader = conf.WithSecrets(loader,
    conf.EnvProvider(),                      // ${env:VAR} — process environment
    conf.DotEnvProvider(".env", ".env.local"), // ${dotenv:VAR} — .env files
)
```

Configuration file example:

```yaml
database:
  host: "${dotenv:DB_HOST}"
  password: "${env:DB_PASSWORD:-dev123}"
```

### .env File Format

```bash
# Comment
DB_HOST=localhost
DB_PORT=5432
DB_PASSWORD="s3cr3t"    # Quoted values supported
API_KEY='abc-123'       # Single quotes
MULTILINE="line1\nline2" # Escapes
```

## Extension: Register a Custom Configuration Center

Implement the `conf.ConfigCenter` interface and register it in `init()`:

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

Usage:

```go
import _ "your-project/mycc"
loader, _ := conf.New("mycc://host/key")
```
