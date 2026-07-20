# Changelog

本文件记录 Beauty 框架与配套工具链（`tools/`）的重要变更。

格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，
提交遵循 [约定式提交](https://www.conventionalcommits.org/zh-hans/)。
框架本身尚未打 tag，故以 `Unreleased` + 日期分组；`tools` 单独维护语义化版本
（见 [tools/README.md](tools/README.md) 的更新日志）。

## [Unreleased]

### Added
- **authz**：新增 `pkg/authz`——授权机制,补齐"只认证不授权"的空白(在 `middleware/auth`/`token`
  确认身份+角色之上,判"能否对某资源做某动作")。`Subject`(id/角色/属性,放 context)+ `Enforcer`
  接口(`Authorize(sub,action,resource)`→nil/ErrDenied)+ 内置 **RBAC**(`Grant` + 通配 `*` / `/*`
  前缀,零依赖)+ HTTP 中间件 / gRPC 一元拦截器(无主体→401/Unauthenticated、拒绝→403/PermissionDenied,
  action/resource 由 mapper 从请求推导)。复杂策略(ABAC/动态/关系授权)由实现同一 `Enforcer` 的 contrib
  提供(`contrib/casbin`、`contrib/openfga`),调用点不变。`-race` 单测覆盖 RBAC 通配/context/HTTP/gRPC。
  - **`contrib/casbin`**——用 casbin/v2 实现 `authz.Enforcer`(RBAC 域/继承、ABAC、策略文件/DB adapter)。
    默认按 Subject 每个角色 Enforce,`WithSubjectID`/`WithMapper` 可调映射。进程内 model+policy 单测,`-race` 通过。
  - **`contrib/openfga`**——用 OpenFGA(Zanzibar 式 ReBAC)实现 `authz.Enforcer`,经 Check API 判定细粒度
    关系权限;默认映射 user/relation/object,`WithMapper` 可调。httptest 打桩单测,`-race` 通过。
  - casbin/openfga 实现核心 `pkg/authz.Enforcer` 故 `require github.com/rushteam/beauty`(核心 v0.2.0 起含 authz)。
- **syncx**：新增 `pkg/syncx`——一组便捷的并发原语(泛型,仅依赖 stdlib + `golang.org/x/sync`),补齐
  常被手搓、易写错的模式:`Map`/`ForEach`(带并发上限 + 错误聚合,首错取消其余)、`SingleFlight`
  (相同 key 去重合并,防缓存击穿)、`Batcher`(按大小/时间 flush 批处理)、`Debounce`/`Throttle`
  (去抖/限频)、`Future`/`Async`(异步跑 + Await 取结果,panic 转错误)。全部 `-race` 单测通过。
- **buildinfo**：新增 `pkg/buildinfo`——运行时暴露构建信息(版本/commit/构建时间/Go 版本/模块/dirty),
  用于 `/version` 端点、启动日志、诊断。零依赖(仅标准库)。两来源自动合并:ldflags 注入的包级变量
  (`-X .../buildinfo.version=...`)优先,缺失项用 `runtime/debug.ReadBuildInfo` 的 VCS 元数据回退。
  `Get()`/`String()`(单行摘要)/`Handler()`(输出 JSON 的 `http.Handler`)。单测覆盖默认/ldflags 覆盖/Handler。
- **contrib**：新增 `contrib/` 多模块区,放**依赖较重**的可选集成——每个子目录是**独立 Go 模块**
  (`github.com/rushteam/beauty/contrib/<name>`,自带 go.mod,与已有的 `tools/` 同套路)。核心
  `github.com/rushteam/beauty` 只留轻量机制/接口,重依赖实现进 contrib:不 import 就零负担、可自己
  照接口实现、可独立钉版本;核心 `go build ./...` 不编译 contrib(模块边界隔离),依赖与 CI 不受影响。
  首个模块 **`contrib/gorm`**——GORM 集成(读写分离 dbresolver、otelgorm 链路、gorm→slog 日志桥含
  慢查询告警、连接池、`TranslateError`+`IsDuplicatedKey` 错误映射、`Write()`/`Read()` 切主从、
  `Ping`/`Close`)。`Open`(MySQL DSN)/`OpenWith`(任意 `gorm.Dialector`,支持 Postgres/SQLite/测试)。
  不 import 核心,仅依赖标准库 slog + OTel 全局 Provider,亦可脱离框架单用。sqlite 内存 + 合成错误
  单测覆盖 CRUD/读写句柄/唯一键判定,`-race` 通过。
  - **`contrib/nats`**——`pkg/mq` 的 NATS broker 绑定,实现 `Publisher`/`Subscriber`:topic→subject、
    `mq.WithGroup`→NATS queue group(竞争)/ 不设组扇出、Headers 与 Key 透传、订阅随 ctx 退订;
    at-most-once(要持久化用 JetStream)。内嵌 `nats-server` 做真实往返单测(扇出/队列组/退订),`-race` 通过。
  - **`contrib/kafka`**——`pkg/mq` 的 Kafka broker 绑定(segmentio/kafka-go),实现 `Publisher`/`Subscriber`:
    topic/Key/Headers 映射、consumer group=mq group、**at-least-once**(handler 成功后提交 offset);Kafka
    消费必须带 group(否则 `ErrGroupRequired`)。单测覆盖消息映射与 group 前置校验(broker 互操作需外部环境)。
  - **`contrib/elasticsearch`**——薄封装 go-elasticsearch/v8:`New`/`Ping`/`Search`(原始 JSON)/`Index`/`ES()`,
    独立不依赖核心。httptest 打桩单测(含 v8 product-check 头),`-race` 通过。
  - **`contrib/natsjs`**——`pkg/mq` 的 NATS **JetStream** 绑定,持久化 + **at-least-once**:`EnsureStream` 建流、
    Publish 落盘、`mq.WithGroup`→durable consumer(竞争、断线续)/ 不设组 ephemeral(扇出)、AckExplicit
    (成功 Ack / 失败 Nak 重投)。内嵌开启 JetStream 的 `nats-server` 做真实往返单测(先发后订的持久化、Nak 重投),`-race` 通过。
  - **`contrib/llm`**——面向 AI 应用的 provider 无关 LLM 客户端(**纯标准库、零外部依赖**,HTTP 直连
    provider REST,不引 SDK)。`Client`(Generate/Stream 流式 token)+ `Embedder` 接口;子包
    `llm/openai`(chat + embeddings,BaseURL 可对接兼容网关)、`llm/anthropic`(messages);中间件
    `Fallback`(跨 provider 切换)/`Retry`/`Metered`(用量+延迟回调,接 OTel/账单由你定,故不绑 OTel)。
    httptest 打桩单测覆盖 Generate/Stream/Embed/Fallback/Metered,`-race` 通过。多厂商:智谱/Kimi/
    MiniMax/通义千问(DashScope)/DeepSeek 等 **OpenAI 兼容**端点换 `WithBaseURL` 即用(提供
    `BaseURL*` 常量);**Azure OpenAI** 用 `NewAzure`(api-key 头 + deployment 路径 + api-version);
    AWS Bedrock 因 SigV4 + 按模型报文需独立适配(不在本模块)。
  - **`contrib/mcp`**——Model Context Protocol 集成,薄封装官方 `modelcontextprotocol/go-sdk`:把服务
    暴露成 AI 工具/资源/提示(`AddTool[In,Out]` 由 SDK 泛型**自动反射 JSON Schema**)、`HTTPHandler`
    (Streamable HTTP,挂 webserver)、`NewStdioService`(结构上满足 `beauty.Service`);客户端 `DialHTTP`/
    `DialCommand`/`Connect` + `ListTools`/`CallTool`。InMemory + httptest 端到端单测,`-race` 通过。不 import 核心。
  - **`contrib/vector`**——RAG / 语义检索的向量存储抽象(**纯标准库、零外部依赖**)。`Store` 接口
    (Upsert/Query topK/Delete)+ `MemoryStore`(暴力余弦,并发安全,dev/小规模直用)+ `Cosine` 助手;
    配 `contrib/llm` 的 Embedder 搭 RAG,大规模换 pgvector/qdrant 实现同接口。单测覆盖余弦与增查删/降序/混维。
  - **`contrib/sqldb`**——`database/sql` 读写分离 + OTel,配合 **sqlc**(生成的 `Queries` 吃 `DBTX`,本模块
    `Writer()`/`Reader()` 即 `DBTX`)/ sqlx / 手写 SQL。`Open`(主 + 多副本,otelsql 埋点、连接池)、`Writer()`
    (主)/`Reader()`(副本轮询,无副本回退主)/`Primary()`(开事务/迁移)/`Ping`/`Close`;`RW()` 自动路由
    (Exec→主、Query→从)+ `Primary(ctx)` 逃生口(应对 `INSERT...RETURNING`/`SELECT...FOR UPDATE` 的路由坑)。
    不 import 驱动与核心。双内存 sqlite 库单测验证路由落点,`-race` 通过。
  - 其中 nats/natsjs/kafka 实现核心 `pkg/mq` 接口故 `import` 核心(`replace => ../..` 本仓解析);gorm/sqldb/elasticsearch 独立。
- **mq**：新增 `pkg/mq`——传输无关的消息队列抽象,补齐框架跨服务异步的空白(此前只有进程内
  `eventbus` 扇出 + `webhook` HTTP 推)。`Publisher`/`Subscriber` 接口(订阅按 ctx 绑定生命周期,
  同时适配 NATS push 与 Kafka pull 语义)+ `Consumer`(把一组订阅包成 `beauty.Service`:
  Start/String/Ready,随 app 停机)+ 处理中间件(`Chain`/`Recover`/`Retry`)。**队列组**
  (`WithGroup`)竞争消费用于多副本水平扩展,不设组则扇出(对齐 NATS queue group / Kafka consumer
  group)。自带**零依赖进程内实现** `NewInProc`(channel + 轮询),用于单体/开发/测试;真 broker
  作为 opt-in 子包实现同一接口,不强引依赖。序列化/trace 透传/分区键/broker 选型是 policy;投递
  保证由 broker 决定(进程内为 at-most-once,用 Retry 兜瞬时错误;可靠"改库+发消息"的 Outbox
  依赖持久层暂未做)。示例 `examples/mq`。单测覆盖扇出/队列组负载均衡/订阅随 ctx 解除/Consumer
  生命周期/Retry/Recover/关闭,`-race` 通过,无新依赖。
- **shard**：新增 `pkg/shard`——有状态服务多副本分片路由的薄机制。用一致性哈希(复用
  `pkg/loadbalance.ConsistentHash`)把每个 key(streamKey/roomID/userID)确定性归属到某实例:
  `Sharder.Owner(key)`/`IsLocal(key)`,成员集可随服务发现动态 `SetMembers`。`Router` 是
  `http.Handler`,按 key 把非本地请求反向代理给归属实例(WebSocket 亦可,带防环标记头),本地 key
  交本地 handler——从而让 `media.Hub`、`webrtc/sfu` 房间、`gameloop` 房间、`presence` 这些进程内
  单实例服务水平扩多副本而无需改成分布式(分片层坐在前面)。成员来源(服务发现)、key 提取
  (`PathHeadKey` 与 Hub 的 `/{key}/…` 对齐)、权重都是 policy。单测覆盖归属稳定/唯一、空集为本地、
  反代到 owner、防环、成员变更迁移,`-race` 通过。
- **docs**：新增 `docs/media-validation.md`——媒体链路**真机验证清单**(RTMP→LL-HLS、多路 Hub、
  WHIP/WHEP、SFU 会议、分片)。单测只验证产出结构,本清单给出可照抄的 ffmpeg 命令 + 真实播放器
  (Safari/hls.js/ffplay)检查点 + 延迟/断流/泄漏红线,上线前照跑。
- **media/hlsmux**：新增 `pkg/media/hlsmux`——RTMP 采集(H.264+AAC)→ HLS 的主力路径,把 FLV 喂给
  `bluenviron/gohlslib`(新增 `gohlslib/v2` + `mediacommon/v2` 依赖),产出产线级
  HLS:MPEG-TS / fMP4 / **LL-HLS(低延迟)**。由 gohlslib 负责分片、播放列表(含 LL-HLS
  `EXT-X-PART`/`PRELOAD-HINT` 与阻塞式刷新)、fMP4 init 段等全部细节。`Bridge` 同时实现
  `rtmp.Handler`(收流)与 `http.Handler`(播放,拿到 SPS/PPS+首关键帧后惰性起 muxer,未就绪回
  503),幂等 `Finish()` 满足 `media.Stream` 可直接进 `pkg/media.Hub` 做多路管理,亦可作为
  `hls.Master` 的一路 ABR 变体(`http.Handler`)。与 `pkg/hls` 分工:RTMP→HLS 走本包;需要
  **通用分片入口**(喂 ffmpeg 现成分片)、**跨码率 ABR 主清单**、**可插拔对象存储 `Store`** 时用
  `pkg/hls`。存储:gohlslib 默认内存,`WithDirectory` 落盘给 CDN(LL-HLS variant 不支持落盘)。
  边界照旧——分片/LL-HLS 参数经 Option 透出,转码/鉴权/多码率在框架外。示例
  `examples/live-hls-gohlslib`(单路)、`examples/live-multi`(多路 + OTel 指标)。单测覆盖 FLV
  解析纯函数 + 用真实 SPS/PPS/ASC 喂帧由 gohlslib 自身校验产出、以及 Hub/ABR 集成,`-race` 通过。
- **media/webrtc/sfu**：在 `pkg/media/webrtc` 之上新增 `sfu` 子包——多人实时音视频的
  「会议室」SFU 原语(选择性转发,不混流不转码)。补齐 WHIP/WHEP 覆盖不到的 **N↔N 动态
  成员**那一档:每人推自己的轨道、订阅其他所有人,成员进出由服务端重协商。**无 glare 模型**:
  客户端只在加入时发一次 offer,此后所有重协商一律由服务端作为 offerer 发起(`resync` 在信令
  未落定时自动推迟、answer 到达后重试)。信令**传输无关**——`Room.Join(id, send)` 通过 `send`
  回调推信令、`Participant.HandleSignal` 吃客户端信令,承载(WebSocket 等)由上层决定。鉴权、
  房间划分、音/视频、混流(MCU)、主讲人检测、STUN/TURN、录制全留 policy。示例
  `examples/webrtc-voice-room`(pkg/ws 信令 + 浏览器多人语音)。进程内多方单测覆盖真实 ICE +
  双向收流 + 重协商 + 离开回收,`-race` 通过。
- **media/webrtc**：新增 `pkg/media/webrtc`——WebRTC 的 WHIP(采集)/WHEP(分发)薄机制,
  基于纯 Go 的 pion/webrtc(零 cgo,新增 `pion/webrtc/v4` + `pion/rtp` 依赖)。面向**亚秒级、
  交互式**实时媒体(连麦/云游戏/实时协作),和 `pkg/hls`(多秒级、可过 CDN 分发)互补,
  与 `pkg/quic`+`pkg/gameloop` 同属「实时」家族。WHIP/WHEP 本质同为「HTTP 一发一答 SDP 协商、
  服务端做 answerer」:`NewWHIP`/`NewWHEP` 是 `http.Handler`(挂 `beauty.WithWebServer` 即可,
  不自起监听),负责 SDP/ICE 协商 + 资源生命周期(`Location`/`DELETE`/断连自动回收);
  `Answer` 暴露协商原语,`Pipe` 做 RTP 纯包转发(不转码,SFU 最小原语),`NewLocalTrackFor`
  按远端编解码建可写轨道,`NewAPI` 装配默认编解码 + 拦截器。鉴权、转发拓扑(SFU/MCU)、
  STUN/TURN、编解码档位、CORS 全留 policy。示例 `examples/webrtc-whip-whep`(一推多播最小 SFU,
  浏览器/OBS 推流 → 多浏览器播放)。端到端单测覆盖真实 ICE 环回 + RTP 转发 + DELETE 拆除。
- **media/hls**：新增 `pkg/hls`——直播/点播 HLS origin(滚动分片窗口 + m3u8 播放列表
  生成 live/VOD + `http.Handler` 挂 webserver 分发)。纯 Go 零 cgo,**不做编解码/切片**,
  分片由上游(ffmpeg 或其它 muxer)`Append` 进来。支持 TS 与 fMP4(`EXT-X-MAP` init
  分片)。**分片存储可插拔**:`Store` 接口 + 内存/磁盘实现(`WithStore`),对象存储可自实现。
  **ABR 多码率**:`Master`/`Variant` 生成 `#EXT-X-STREAM-INF` 主清单并按变体名路由,变体
  Handler 可接 `*Stream` 或 `http.FileServer`(适配进程内或 ffmpeg 外部转码产物)。
  **LL-HLS 低延迟**:`WithPartTarget` 开启部分分片(`AppendPart`/`CompleteSegment`),播放列表
  带 `EXT-X-PART`/`PART-INF`/`SERVER-CONTROL`/`PRELOAD-HINT` 并支持阻塞式刷新
  (`_HLS_msn`/`_HLS_part`)。示例:`examples/hls`(合成分片)、`examples/live-transcode`
  (拓扑 A:RTMP→ffmpeg 多码率转码→ABR HLS)。
- **media/rtmp**：新增 `pkg/media/rtmp`——RTMP 采集服务端,薄封装 `yutopp/go-rtmp`
  (新增依赖)。接受 OBS/ffmpeg 推流,把每路流的 metadata/audio/video(FLV tag body)
  交给业务 `Handler`;`Server` 结构上满足 `beauty.Service` 可直接 `WithService` 挂进框架。
  **鉴权**:连接级 `WithConnectAuth`(按 app/tcUrl)+ 推流级 `PublishFunc` 返回 nil 拒绝。
  只负责收流,不转码。
- **media**：新增 `pkg/media`——直播/视频服务的编排薄机制。`Hub[S media.Stream]` 做**多路流管理**
  (streamKey→Session 注册表 + 生命周期 + 按 key 路由 + 防重复推流,`Session.Context`
  供外部后台任务随流停机);按流类型泛型化,`Stream = http.Handler + Finish()`,可承载
  `*hlsmux.Bridge`(gohlslib)或 `*hls.Stream`(自研 origin),故 `pkg/media` 不依赖 `pkg/hls`。
  `Supervisor` 做**子进程监督**(ffmpeg 等,启动/按 `pkg/backoff`
  退避重启/优雅停,命令构造留 policy);`Metrics` 基于 OTel 全局 Meter 上报运维指标
  (`media.streams.active` / `publish` / `rejected` / `ingest.bytes` / `segments` /
  `transcode.restarts`,未配 telemetry 时 no-op)。示例 `examples/live-multi`(多路 + 指标)。
- **quic**：新增 `pkg/quic`——基于 quic-go 的连接层,作为 `pkg/ws`(WebSocket/TCP)
  之外面向实时/游戏同步的可选传输(opt-in 子包,新增 `quic-go` 依赖)。一条连接同时
  提供**可靠有序流**(`OpenStream`/`AcceptStream`,多路复用、跨流无队头阻塞——关键指令)
  与**不可靠数据报**(`SendDatagram`/`ReceiveDatagram`,RFC 9221——高频状态/位置更新,
  丢了不重传不阻塞)。`Server` 结构上满足 `beauty.Service`,可直接 `WithService` 挂进
  框架;`Dial` 客户端;`DevTLSConfig` 提供开发用自签证书(生产用 `WithTLSConfig`)。
  性能相关:`ListenUDP` 建 socket 时尽力把收发缓冲提到 7MB,`WithPacketConn` /
  `WithTransport` / `WithDialTransport` 支持自备(缓冲调优的)UDP socket 与
  `quic.Transport` 复用(一条 socket 同承载监听 + 拨号),包注释附 sysctl 提示。
  端到端示例见 `examples/statesync-quic`(指令走可靠流、状态走不可靠数据报)。
- **gameloop**：新增 `pkg/gameloop`——「机制而非策略」的定步长游戏循环原语(定步长
  tick、并发输入聚合、经 `stream.Broadcaster` 扇出、结构上满足 `beauty.Service`
  可直接 `WithService` 挂进框架)。帧同步/状态同步的服务端骨架,同步策略全在
  `Handler.OnTick` 里、不进框架。仅依赖 `pkg/stream`。两个参考示例展示同一个
  `Room` 换 `OnTick` 即切换策略:`examples/gameloop`(帧同步/lockstep,下发输入,
  bot 自校验逐帧输入全端一致)、`examples/statesync`(状态同步,服务器权威模拟 +
  `pkg/spatial` AOI 视野过滤,下发状态)。
- **dlock**：新增 URL/DSN 工厂，与 `conf.New` 的 scheme 工厂模式对齐——
  `dlock.New(dsn)` 构造 `Locker`、`dlock.NewElector(dsn)` 构造 `Elector`，空导入对应
  infra 子包即注册（`etcd`/`etcdv3`、`consul`、`redis` 注册两者；`k8s` 只注册 Elector）。
  cron 的 `WithLeaderElector` 可直接吃 DSN 构造出的 elector。
- **kvstore**：`pkg/infra/etcd` 新增 `kvstore.Store` 的 etcd 实现（lease 做 TTL、
  可精确查剩余；事务/CAS 做 SetNX/Incr）。与 redis 实现互补——etcd 秒级 TTL、
  已有 etcd 免再引 Redis；redis 毫秒级 TTL、`Incr` 原生原子。
- **docs**：新增 `pkg/infra/README.md`——基建适配层总览（能力矩阵 + 各后端
  conf/dlock/kvstore/discover 的 DSN 与最小用法）。
- **dlock**：分布式锁/选主后端补齐两家——
  - **consul**（`pkg/infra/consul`，零新依赖）：基于 session + KV Acquire 实现
    `Locker`（含真非阻塞 `TryLock`）+ `Elector`，`RenewPeriodic` 续期,`NewDLock` /
    `NewDLockFromConfig` 构造;
  - **redis**（`pkg/infra/redis`，新增 `go-redis/v9` 依赖）：单节点 SET NX PX +
    Lua CAS 释放/续期实现 `Locker` + `Elector`(如实标注非 Redlock 的单节点语义)。
- **kvstore**：`pkg/infra/redis` 新增 `kvstore.Store` 的 Redis 实现,给
  counter/cooldown/idempotency 等原语一个真正跨实例共享的后端。
- **conf**：新增基于 k8s ConfigMap / Secret 的配置中心后端（opt-in 子包
  `pkg/infra/k8s`，空导入注册 `configmap` / `secret` scheme）。纯 k8s 部署无需额外
  运维 nacos/etcd 即可热更新配置——`conf.New("configmap://<ns>/<name>/<dataKey>")`，
  按 `metadata.name` watch 目标资源、断线指数退避重连、按值去重避免无谓重载。
- **logger**：`pkg/service/logger` 新增 `NewTraceHandler`，包装任意 `slog.Handler`，
  在使用 `slog.*Context` 记录日志时自动注入当前 span 的 `trace_id` / `span_id`。
- **grpcclient**：客户端 xDS 支持——`xds:///service` 目标，opt-in 子包
  `pkg/client/grpcclient/xds`（空导入注册 resolver，`WithCredentials()` 提供 xDS mTLS）。
- **tools**：脚手架新增 `clean` 整洁架构模板（domain/application/adapter/infra/bootstrap
  四环 + user 示例），`beauty add handler|job` 增量生成器，`dev --watch` 热重载，
  `--module` / `--dry-run` / `--with-docker` / `--with-k8s` / `--with-ci` 选项，
  生成项目内置版本注入（`VERSION` + `-ldflags -X`）与统一 Makefile。

### Changed
- **ci**：新增 GitHub Actions(`.github/workflows/ci.yml`):core 作业跑 gofmt/vet/build/`go test -race`
  + **校验核心不 import contrib** + govulncheck;contrib 作业按模块矩阵(9 个)各自 gofmt/vet/test/
  govulncheck;tools 作业 build/vet。配套对全仓做了一次 `gofmt` 统一。
- **infra/redis**：`NewClient` 支持 OTel 埋点——新增 `WithTracing()`/`WithMetrics()` 选项,基于
  `redisotel` 给每条 redis 命令产生 span 与命令级 metrics(用 beauty telemetry 配好的全局
  Tracer/Meter Provider,未配则 no-op);`Config` 补 `PoolSize`/`MinIdleConns`/`DialTimeout`/
  `ReadTimeout`/`WriteTimeout` 连接池与超时字段(零值用 go-redis 默认)。分布式锁/KV 共用的
  客户端从此可观测。新增依赖 `redisotel`(其依赖 go-redis/otel 核心已有),go-redis 升至 v9.21。
- **client**：把已有的韧性原语接进**直连/普通客户端**(此前只有服务发现版客户端接了节点级
  熔断+重试)。HTTP `resty.NewHTTPClient` 新增 `WithRetry`(基于 `pkg/backoff.Policy`:默认只重
  试幂等方法的瞬时失败——网络错误/429/502/503/504,遵守 `Retry-After`、请求体自动重放)、
  `WithRetryable`(自定义判定)、`WithCircuitBreaker`(复用 `middleware/circuitbreaker` 的
  RoundTripper);传输链为**熔断→重试→otel→base**(熔断最外:一次逻辑请求一个样本,打开即短路
  不进重试)。gRPC `grpcclient` 新增 `WithCircuitBreakerInterceptor`(接入请求级熔断拦截器,直连
  与发现模式均生效),与发现版的节点级 `WithCircuitBreaker` 互补;gRPC 重试默认已由 service config
  开启。单测覆盖重试/不重试幂等性/体重放/ctx 取消/熔断短路,`-race` 通过,无新依赖。
- **infra/discover**：收敛各后端的 client 构造——`pkg/infra/{etcd,consul,k8s}` 各暴露
  `NewClient` / `NewClientset`，`pkg/service/discover/{etcdv3,consul,k8s}` 改为复用
  （消除与配置中心/分布式锁重复的连接构造，对齐 nacos 早已复用 `infra/nacos` 的做法）。
- **dlock/k8s**：`pkg/infra/k8s` 新增 `NewElectorFromConfig`（从 kubeconfig/集群内配置
  直接建 clientset），对齐 `etcd.NewDLockFromConfig` 的便捷构造。
- **tools**：升级到 `tools` v0.1.0；生成的 `go.mod` 升级 `go 1.26` 与当前 beauty 版本；
  模板 logger 默认接入 `NewTraceHandler`，生成项目日志自动带 trace 关联。

### Security
- **toolchain**：go.mod 固定 `toolchain go1.26.5`，修掉 govulncheck 判定为**可达**的
  8 个 Go 标准库漏洞(crypto/tls、crypto/x509、net/http HTTP/2 无限循环、html/template
  XSS 等)。修复后 `govulncheck ./...` 可达漏洞归零(其余 dependabot 告警均为不可达的
  间接依赖噪音)。
- **contrib/toolchain**：为全部 contrib 模块(gorm/sqldb/elasticsearch/nats/natsjs/kafka/llm/
  vector/mcp)固定 `toolchain go1.26.5`,消除各自 govulncheck 判定为可达的标准库漏洞。全仓
  (核心 + 全 contrib)`govulncheck` 可达漏洞归零;GitHub 提示的 14 个依赖告警经核实均为**不可达**噪音。

### Fixed
- **tools**：修复模板与框架 API 漂移导致生成项目无法编译的问题（中间件接口、
  注册中心字段、cron 模板坏死包、unified 服务组合裁剪等）。详见
  [tools/README.md](tools/README.md) 的 v0.1.0 条目。

> 注：使用 `tools` 生成依赖上述 `logger.NewTraceHandler` 的项目时，
> 需待该框架变更发布后（脚手架会执行 `go get github.com/rushteam/beauty@latest`）方可拉取到。

## 近期亮点（按提交）

- `feat(config)`：`WithConfig` 配置热重载与示例。
- `feat(webhook)`：事件驱动的 webhook 通知器。
- `feat(featureflag)`：本地特性开关与 A/B 实验。
- `feat(vars)`：`${KEY}` / `${KEY:-default}` 配置插值。
- `feat(middleware/callbacks)`：将 callbacks 切面接入 http/grpc。
- `feat(sse,ws)`：基于 `stream.Broadcaster` 的广播辅助。
- `feat(options)`：分层函数式选项。
- `feat(stream)`：面向多订阅者的扇出广播器。
