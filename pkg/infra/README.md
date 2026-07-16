# Beauty 基建适配层（`pkg/infra`）

`pkg/infra` 把各类中间件/编排平台（etcd、Consul、Kubernetes、Redis、Nacos、
Polaris）适配到框架定义的**后端无关接口**上。核心接口都在纯标准库的接口包里
（`pkg/conf`、`pkg/dlock`、`pkg/kvstore`），infra 子包才引入对应 SDK 依赖——业务
按需 import，不用的后端不进依赖树。

设计约定：

- **接口在别处，实现在这里**：`conf.ConfigCenter` / `dlock.Locker`+`Elector` /
  `kvstore.Store` 由 infra 子包实现，不重新发明分布式算法，只做薄封装。
- **一处连接构造**：每个后端只有一处 `NewClient` / `NewClientset`，配置中心、
  分布式锁、KV 存储、服务发现共用它（见各包 `client.go` / `config_client.go`）。
- **URL/DSN 工厂**：`conf.New(dsn)` 与 `dlock.New(dsn)` / `dlock.NewElector(dsn)`
  按 scheme 选后端——**空导入对应子包**即可注册（`import _ ".../pkg/infra/etcd"`）。
- **如实标注语义**：能力和局限写在包注释里（如 Redis 锁是单节点语义、非 Redlock；
  etcd kvstore 的 TTL 粒度是秒）。不假装提供做不到的强度。

## 能力矩阵

| 后端 | 配置中心 `conf` | 分布式锁/选主 `dlock` | TTL-KV `kvstore` | 服务发现 `discover` |
|---|---|---|---|---|
| **etcd** | ✅ `etcd` / `etcdv3` | ✅ Locker + Elector | ✅ | ✅ `etcdv3` |
| **consul** | ✅ `consul` | ✅ Locker + Elector | — | ✅ |
| **k8s** | ✅ `configmap` / `secret` | ✅ 仅 Elector¹ | — | ✅ |
| **redis** | — | ✅ Locker + Elector² | ✅ | — |
| **nacos** | ✅ `nacos` | — | — | ✅ |
| **polaris** | ✅ `polaris` | — | — | ✅ |

> ¹ k8s 基于 Lease 的 leaderelection 是「选主」语义,没有互斥锁原语,故只实现 `Elector`。
> ² Redis 锁是**单节点语义**(SET NX PX + Lua CAS),不是跨多 master 的 Redlock。
> 需要更强保证时用 etcd / consul 后端。

## 配置中心 `conf`

统一入口 `conf.New(dsn)`,scheme 决定后端。远程后端需**空导入**对应子包触发注册:

```go
import (
    "github.com/rushteam/beauty/pkg/conf"
    _ "github.com/rushteam/beauty/pkg/infra/etcd" // 注册 etcd/etcdv3 scheme
)

loader, err := conf.New("etcd://127.0.0.1:2379/myapp/config.yaml")
loader.Unmarshal(&cfg)
loader.Watch(ctx, func() { loader.Unmarshal(&cfg) }) // 热更新
```

各后端 DSN:

```text
etcd://[user:pass@]host1,host2/key?dial_ms=3000
consul://[:token@]host:port/kv/path?datacenter=dc1&namespace=ns
configmap://<namespace>/<name>/<dataKey>?kubeconfig=/path     # 纯 k8s 部署免运维额外配置中心
secret://<namespace>/<name>/<dataKey>?kubeconfig=/path
nacos://host:8848/dataId?namespace=dev&group=DEFAULT_GROUP
polaris://host:8090/key
```

## 分布式锁 / 选主 `dlock`

两个原语:`Locker`(互斥锁,`Lock`/`TryLock`)与 `Elector`(持续选主,当选期间持有
`leaderCtx`,失去 leader 时被 cancel)。典型用途——多实例部署下让 Cron **只在 leader 上跑**:

```go
import (
    "github.com/rushteam/beauty/pkg/dlock"
    _ "github.com/rushteam/beauty/pkg/infra/etcd" // 注册 etcd 后端
)

elector, err := dlock.NewElector("etcd://127.0.0.1:2379/?ttl=10s")
cron := cron.New(cron.WithLeaderElector(elector, "myservice-cron"), /* handlers... */)
```

也可直接用互斥锁:

```go
locker, _ := dlock.New("redis://:pass@127.0.0.1:6379/0?ttl=15s")
lock, err := locker.Lock(ctx, "job:reindex")
defer lock.Unlock(ctx)
```

各后端 DSN(锁的 key 在调用 `Lock`/`Run` 时传入,不在 DSN 里;通用 query:`prefix`、`ttl`):

```text
etcd://[user:pass@]host1,host2/?ttl=10s&prefix=/beauty/dlock/&dial_ms=3000
consul://[:token@]host:port/?ttl=15s&prefix=beauty/dlock/&datacenter=dc1&identity=host-a
redis://[:password@]host:port/db?ttl=15s&retry=100ms&prefix=beauty:dlock:
k8s://?namespace=prod&kubeconfig=/path&identity=pod-a               # 仅 Elector
```

> 不想用 DSN 时,每个后端也有直接构造函数:`etcd.NewDLock` / `NewDLockFromConfig`、
> `consul.NewDLock*`、`redis.NewDLock*`、`k8s.NewElector` / `NewElectorFromConfig`。
> 单机/测试用 `dlock.NewMemory()`(进程内,无需任何后端)。

## TTL-KV 存储 `kvstore`

`kvstore.Store` 给 counter / cooldown / idempotency 等原语一个**跨实例共享**的后端
(默认是单进程内存,水平扩展时状态会散落各实例)。方法都是原子操作:`Incr`、
`SetNX`、`Get`/`GetInt`、`Set`、`TTL`、`Delete`。

```go
import beautyredis "github.com/rushteam/beauty/pkg/infra/redis"

store := beautyredis.NewStore(beautyredis.NewClient(&beautyredis.Config{Addr: "127.0.0.1:6379"}))
// 或 etcd:  beautyetcd.NewStoreFromConfig(&beautyetcd.Config{Endpoints: []string{...}})
n, _ := store.Incr(ctx, "quota:user:42", 1, time.Minute)
```

- **redis**:基于 `PEXPIRE`,支持**毫秒级** TTL,`Incr` 原生原子。适合精确/短冷却。
- **etcd**:基于 lease,TTL 粒度是**秒**(不足 1s 抬到 1s),`Incr` 走事务 CAS。
  `TTL()` 可精确查询剩余时间;适合已有 etcd、不想再引 Redis 的场景。

## 目录一览

| 子包 | 内容 |
|---|---|
| `etcd/` | `client.go`(连接)、`config_client.go`、`dlock.go`、`kvstore.go`、`factory.go` |
| `consul/` | `config_client.go`(含连接)、`dlock.go`、`factory.go` |
| `k8s/` | `client.go`、`config_center.go`、`dlock.go`(Elector)、`factory.go` |
| `redis/` | `client.go`、`dlock.go`、`kvstore.go`、`factory.go` |
| `nacos/` | `config.go`、`config_client.go`、`nacos.go`、`factory.go` |
| `polaris/` | `config_client.go`、`factory.go` |

服务发现(`discover`)的注册中心适配在 `pkg/service/discover/{etcdv3,consul,k8s,nacos,polaris}`,
其连接构造复用本目录对应后端的 `NewClient` / `NewClientset`。
