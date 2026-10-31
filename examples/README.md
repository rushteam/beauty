# examples —— 可运行的单一职责示例

每个子目录是一个**独立 `main.go`**,演示一个能力的最小用法,是最快看懂某个 API 怎么用的入口。

```bash
go run ./examples/<name>          # 例:go run ./examples/roaring
```

> 主题级文档(设计说明 + 相关包 + 对应示例)见 [`docs/README.md`](../docs/README.md) 的索引表。本页按能力分组速查。

## 基础数据结构与算法(`pkg/foundation` · `pkg/idgen`)

详见 [data-structures](../docs/data-structures.md) / [data-structures-en](../docs/data-structures-en.md)。

| 示例 | 演示 | 相关包 |
|---|---|---|
| [filter](filter) | Bloom / Cuckoo 判存去重 | `pkg/foundation/filter` |
| [skiplist](skiplist) | 有序跳表 + 名次 + 范围 | `pkg/foundation/skiplist` |
| [bitmap](bitmap) | 稠密精确位图标记 | `pkg/foundation/bitmap` |
| [roaring](roaring) | 压缩位图(RunOptimize + 官方跨语言格式) | `pkg/foundation/roaring` |
| [intervaltree](intervaltree) | 区间相交 / 点覆盖查询 | `pkg/foundation/intervaltree` |
| [tdigest](tdigest) | 流式分位数(P99)+ 跨进程合并 | `pkg/foundation/sketch` |
| [simhash](simhash) | SimHash / MinHash 近似相似去重 | `pkg/foundation/simhash` |
| [crdt](crdt) | 无冲突复制 + delta-state 增量同步 | `pkg/foundation/crdt` |
| [diff](diff) | LCS / Myers 差异与补丁 | `pkg/foundation/diff` |
| [jsonpatch](jsonpatch) | JSON Patch(RFC6902)+ 三路合并 | `pkg/foundation/diff` |
| [dat](dat) | 双数组 Trie / AC 多模匹配 + 归一化反绕过 | `pkg/foundation/actrie` |
| [actrie](actrie) | Aho-Corasick 敏感词匹配 | `pkg/foundation/actrie` |
| [ringbuffer](ringbuffer) | 环形缓冲区 | `pkg/foundation/ringbuffer` |
| [fsm](fsm) | 有限状态机 | `pkg/foundation/fsm` |
| [dag](dag) | DAG 拓扑排序 + 并发执行 | `pkg/foundation/dag` |
| [keyedmutex](keyedmutex) | 按 key 细粒度互斥锁 | `pkg/foundation/keyedmutex` |
| [geohash](geohash) | Geohash 地理编码 | `pkg/foundation/geohash` |
| [idgen](idgen) | Snowflake 分布式 ID | `pkg/idgen` |
| [segment](segment) | 号段模式分布式 ID(双缓冲) | `pkg/idgen` · `contrib/gorm` |

## 弹性与流控(`pkg/resilience` · `pkg/middleware`)

| 示例 | 演示 |
|---|---|
| [backoff](backoff) | 指数退避重试 |
| [resilience](resilience) | 限流 / 熔断 / 分组限流 |
| [bulkhead](bulkhead) | 隔离舱并发配额(`pkg/resilience/bulkhead` + HTTP 中间件) |
| [cooldown](cooldown) | 冷却计时 |
| [counter](counter) | 分布式计数器 |
| [dberr](dberr) | DB 弹性(熔断 + 舱壁) |

## 服务发现与 RPC(`pkg/service/discover` · `pkg/client`)

| 示例 | 演示 |
|---|---|
| [grpc-service-discovery](grpc-service-discovery) · [grpc-client-discovery](grpc-client-discovery) | gRPC 注册与发现 |
| [grpc-dial-context](grpc-dial-context) · [grpc-label-filter](grpc-label-filter) · [grpc-label-selector](grpc-label-selector) | 定向拨号 / 标签选择 |
| [grpc-registry-plugin](grpc-registry-plugin) · [grpc-registry-plugin-simple](grpc-registry-plugin-simple) · [grpc-xds-client](grpc-xds-client) | 注册插件 / xDS |
| [http-service-discovery](http-service-discovery) | HTTP 客户端服务发现 |
| [loadbalance](loadbalance) · [geo-routing](geo-routing) · [router](router) | 负载均衡 / 地域亲和路由 |
| [spire-grpc](spire-grpc) | SPIFFE/SPIRE mTLS |

## API / 中间件 / 安全

| 示例 | 演示 |
|---|---|
| [api-security](api-security) · [security](security) | 内置 HTTP 中间件组合 |
| [token](token) | 认证与限流 |
| [sensitive-middleware](sensitive-middleware) | 敏感词过滤中间件 |
| [expr](expr) | 规则引擎 / 表达式求值 |
| [featureflag](featureflag) | 功能开关 |
| [idempotency](idempotency) | 幂等 |
| [afterwork](afterwork) · [audit](audit) · [observability](observability) | 善后任务 / 审计 / 可观测 |

## 消息、任务与编排

| 示例 | 演示 |
|---|---|
| [mq](mq) | 消息队列发布订阅 |
| [eventbus](eventbus) | 进程内事件总线 |
| [webhook](webhook) | Webhook 投递 |
| [delayqueue](delayqueue) · [scheduler](scheduler) | 延迟队列 / 调度 |
| [saga](saga) · [txn](txn) | Saga / 事务编排 |
| [cron-leader](cron-leader) | 选主定时任务 |
| [notification](notification) | 通知分发 |

## 存储与共享状态

| 示例 | 演示 |
|---|---|
| [storage](storage) · [kvstore-shared](kvstore-shared) | KV 存储 / 跨实例共享 |
| [ephemeral](ephemeral) | 临时数据(TTL) |
| [tally](tally) | 计数 / 累计 |
| [session](session) | 会话 |
| [config](config) | 配置加载与热更新 |

## 实时通信与媒体(`pkg/transport` · `pkg/media`)

| 示例 | 演示 |
|---|---|
| [websocket](websocket) · [sse](sse) | WebSocket / Server-Sent Events |
| [chat](chat) · [presence](presence) · [party](party) · [group](group) | 聊天 / 在线状态 / 房间 |
| [p2p-signaling](p2p-signaling) | P2P 信令组网 |
| [resume](resume) · [tcp-framing](tcp-framing) | 断点续传 / TCP 分帧 |
| [statesync](statesync) · [statesync-delta](statesync-delta) · [statesync-interpolate](statesync-interpolate) · [statesync-quic](statesync-quic) · [statesync-quic-delta](statesync-quic-delta) | 状态同步(增量 / 插值 / QUIC) |
| [hls](hls) · [live-hls-gohlslib](live-hls-gohlslib) · [live-multi](live-multi) · [live-pk](live-pk) · [live-transcode](live-transcode) | HLS / 直播 / 转码 / 连麦 PK |
| [webrtc-voice-room](webrtc-voice-room) · [webrtc-whip-whep](webrtc-whip-whep) | WebRTC 语音房 / WHIP-WHEP |

## 游戏(`pkg/game`)

| 示例 | 演示 |
|---|---|
| [gameloop](gameloop) · [spatial](spatial) · [pathfind](pathfind) | 游戏循环 / 空间索引 / 寻路 |
| [match](match) · [matchmaker](matchmaker) · [matchmaker-room](matchmaker-room) · [versus](versus) · [tournament](tournament) | 对局 / 匹配 / 对战 / 赛事 |
| [leaderboard](leaderboard) · [leveling](leveling) · [loot](loot) · [questlog](questlog) · [reddot](reddot) · [momentum](momentum) | 排行榜 / 升级 / 掉落 / 任务 / 红点 |
| [clan](clan) · [relationship](relationship) · [wallet](wallet) | 公会 / 社交关系 / 钱包 |
| [agones-room](agones-room) · [slg-world](slg-world) · [voxel](voxel) | Agones 房间 / SLG 世界 / 体素 |

## 网关 / AI / 其他

| 示例 | 演示 |
|---|---|
| [higress-gateway](higress-gateway) · [higress-ai](higress-ai) | Higress 入口 / AI 网关 |
| [graphql-bff](graphql-bff) | GraphQL / BFF |
| [llm-httpui](llm-httpui) · [agentservice](agentservice) | LLM / Agent 服务 |
| [console](console) | Web 远程控制台 |
| [pwa](pwa) | PWA / Service Worker 配套 |
| [protobuf-example](protobuf-example) | Protobuf 集成 |
| [complete](complete) · [services](services) · [status](status) | 综合示例 / 多服务 / 健康状态 |
