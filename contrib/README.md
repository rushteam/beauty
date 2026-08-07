# contrib —— 可选集成(各自独立 Go 模块)

`contrib/` 下每个子目录是一个**独立的 Go 模块**(有自己的 `go.mod`,模块路径
`github.com/rushteam/beauty/contrib/<name>`),用来放**依赖较重**的第三方集成
(GORM、Elasticsearch、Kafka 等)。

## 为什么独立成模块

beauty 核心(`github.com/rushteam/beauty`)只保留轻量、通用的机制与接口。重依赖的具体实现
放进 contrib 独立模块,于是:

- **不用就零负担**:不 import 就不进你的依赖图——核心 `go.mod` 不会因为 gorm/ES 而变重。
- **可自己实现**:contrib 尽量面向核心的接口/约定编写(如 `pkg/mq` 的 `Publisher`、slog、
  OTel 全局 Provider),你完全可以照着自写一份、不用官方 contrib。
- **可钉不同版本**:各 contrib 独立打 tag、独立 `go get`,你能按需锁定版本,和核心解耦升级。

这与仓库里已有的 `tools/`(独立模块 `github.com/rushteam/beauty/tools`)是同一套路。

## 用法

```bash
go get github.com/rushteam/beauty/contrib/gorm@latest
```

各 contrib 在**自己的目录**里构建/测试(独立模块):

```bash
cd contrib/gorm && go test ./...
```

核心仓库的 `go build ./...` / `go test ./...` **不会**编译 contrib(模块边界隔离),
所以核心的依赖与 CI 不受 contrib 影响。

## 当前模块

| 模块 | 能力 | 主要依赖 |
|---|---|---|
| [`contrib/gorm`](gorm) | GORM 集成:读写分离(dbresolver)、otelgorm 链路、slog 日志桥、错误映射 | gorm.io/gorm、driver/mysql、otelgorm |
| [`contrib/sqldb`](sqldb) | database/sql 读写分离 + OTel(otelsql),配合 **sqlc**/sqlx/手写 SQL | XSAM/otelsql |
| [`contrib/nats`](nats) | `pkg/mq` 的 NATS broker 绑定(queue group 竞争 / 扇出;at-most-once) | nats.go |
| [`contrib/natsjs`](natsjs) | `pkg/mq` 的 NATS **JetStream** 绑定(持久化、at-least-once、重投、断线续) | nats.go/jetstream |
| [`contrib/kafka`](kafka) | `pkg/mq` 的 Kafka broker 绑定(franz-go + kotel OTel;consumer group;at-least-once) | twmb/franz-go、plugin/kotel |
| [`contrib/elasticsearch`](elasticsearch) | Elasticsearch 集成:健康 / 搜索 / 写入,暴露原始 JSON | go-elasticsearch/v8 |
| [`contrib/llm`](llm) | provider 无关 LLM 客户端:对话/流式/embedding/**工具调用** + Fallback/Retry/Metered/**Guard 护栏** + 多厂商(OpenAI 兼容 / Anthropic / **AWS Bedrock**)+ 薄 **agent 循环**(`llm/agent`,含审批/流式事件/Steer/Hooks + 统一 Agent 接口/**Planner**/**Team 移交**/**Parallel 并发**/**BestOfN**/**VerifyLoop** 编排 + 工具参数 **JSON 容错**/运行内**上下文压缩**/消息合并)+ **会话记忆** + **Agent Skills** | 无(纯 stdlib) |
| [`contrib/llmsession`](llmsession) | `llm/agent/session.Store` 的 SQLite / Redis 实现 | modernc.org/sqlite、go-redis |
| [`contrib/vector`](vector) | 向量存储 / RAG 语义检索:Store 接口 + 内存实现,配 llm 搭 RAG | 无(纯 stdlib) |
| [`contrib/memoryvector`](memoryvector) | 胶水:Embedder + vector → `llm/agent/memory.Store`(语义长期记忆) | llm + vector |
| [`contrib/mcp`](mcp) | Model Context Protocol:把服务暴露成 AI 工具(server)+ 消费(client),struct→schema 自动反射 | modelcontextprotocol/go-sdk |
| [`contrib/mcpagent`](mcpagent) | 胶水:把 `mcp` 的远程工具桥接成 `llm/agent.Tool`,喂给 agent.Runner 的工具循环 | llm + mcp + go-sdk |
| [`contrib/wasm`](wasm) | WebAssembly 插件运行时(wazero):沙箱化 wasm 模块 + host functions + "HTTP 中间件即 wasm" | tetratelabs/wazero |
| [`contrib/wasmagent`](wasmagent) | 胶水:把 `wasm` 的沙箱执行能力接到 `llm/agent`(技能脚本 wasm 执行 + wasm 模块即 agent.Tool) | wasm + llm |
| [`contrib/wasmopa`](wasmopa) | OPA 策略即 wasm:Rego 编译的 wasm 实现 `pkg/authz.Enforcer`,纯 Go 策略求值 | tetratelabs/wazero |
| [`contrib/casbin`](casbin) | `pkg/authz` 的 Casbin 授权引擎(RBAC 域/继承、ABAC、策略文件/DB) | casbin/v2 |
| [`contrib/openfga`](openfga) | `pkg/authz` 的 OpenFGA 关系授权(ReBAC,细粒度) | openfga/go-sdk |
| [`contrib/spire`](spire) | SPIFFE/SPIRE Workload API:X509-SVID mTLS + SPIFFE ID→auth/authz | go-spiffe/v2 |

`contrib/nats`、`contrib/natsjs`、`contrib/kafka` 实现核心 `pkg/mq` 的 `Publisher`/`Subscriber`
接口,`contrib/casbin`、`contrib/openfga` 实现核心 `pkg/authz.Enforcer` 接口——这些都 `require
github.com/rushteam/beauty`(已对齐发布版本,无 `replace`);`contrib/spire` 对接核心 auth/authz 与
TLS 钩子(本地联调暂用 `replace`,发布前去掉);`contrib/gorm`、`contrib/sqldb`、
`contrib/elasticsearch`、`contrib/llm`、`contrib/vector`、`contrib/mcp` 不依赖核心,可完全独立使用
(其中 `llm`/`vector` 纯标准库、零外部依赖;`mcp` 的 `Service` 结构上满足 `beauty.Service`)。
`contrib/mcpagent` 不依赖核心,但依赖 `llm` 与 `mcp`(胶水模块,`go.mod` 用 `replace` 本地联调)。
`contrib/wasm` 不依赖核心,仅依赖 wazero(纯 Go),可独立使用。

## 版本

各模块独立打 tag(`<模块目录>/vX.Y.Z`),独立 `go get`:

```bash
go get github.com/rushteam/beauty/contrib/gorm@v0.1.0
go get github.com/rushteam/beauty/contrib/sqldb@v0.1.0
go get github.com/rushteam/beauty/contrib/nats@v0.1.0    # 依赖核心 beauty v0.1.0
```

依赖核心的 mq 模块默认按 `require` 的核心版本解析。若本地要同时改核心与该 contrib,临时在其
`go.mod` 加一行 `replace github.com/rushteam/beauty => ../..` 即可(提交/发布前去掉)。
> 注:不建议用覆盖全仓的根 `go.work`——各 contrib 与核心的间接依赖版本可能不一致(如
> `genproto` 新旧拆分),合并工作区会触发 ambiguous import。按需只对"核心 + 单个 contrib"
> 做局部 replace 更稳。

## 约定

- contrib 模块**不得**被核心模块 import(否则依赖就漏进核心了)。
- 优先通过核心的**接口**(如 `mq.Publisher`/`mq.Subscriber`)或**标准约定**(`log/slog`、
  OTel 全局 Provider)与框架协作,尽量少直接依赖核心包;能脱离框架单用更好。
- 边界仍是"机制而非策略":contrib 负责把第三方库按 beauty 的可观测/配置约定接好,
  建模、迁移、业务逻辑留给使用方。
