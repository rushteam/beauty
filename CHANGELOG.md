# Changelog

本文件记录 Beauty 框架与配套工具链（`tools/`）的重要变更。

格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，
提交遵循 [约定式提交](https://www.conventionalcommits.org/zh-hans/)。
框架本身尚未打 tag，故以 `Unreleased` + 日期分组；`tools` 单独维护语义化版本
（见 [tools/README.md](tools/README.md) 的更新日志）。

## [Unreleased]

### Added
- **media/hls**：新增 `pkg/hls`——直播/点播 HLS origin(滚动分片窗口 + m3u8 播放列表
  生成 live/VOD + `http.Handler` 挂 webserver 分发)。纯 Go 零 cgo,**不做编解码/切片**,
  分片由上游(ffmpeg 或 rtmp remux)`Append` 进来。支持 TS 与 fMP4(`EXT-X-MAP` init
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
- **media/remux**：新增 `pkg/media/remux`——把 FLV(H.264/AAC)重封装成 MPEG-TS 分片
  (纯 Go `go-astits`,新增依赖),按关键帧切片 `Append` 到 `hls.Stream`,**不转码**。
  `FLVToHLS` 实现 `rtmp.Handler`,把采集与分发串成端到端直播。示例见 `examples/live-hls`
  (RTMP 推流 → HLS 播放)。单测覆盖 FLV 解析/AVCC→AnnexB/ADTS/切片并校验产出合法 TS。
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
