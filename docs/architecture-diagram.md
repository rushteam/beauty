# 🏗️ Beauty Framework — Architecture

> **一套生命周期跑微服务 —— HTTP · gRPC · 定时 · 实时媒体 · WASM Agent 沙箱**

---

## Publication-Quality Color Palette (Nature/Science)

| 用途 | 色值 | 说明 |
|------|------|------|
| 主色 / 蓝 | `#2166AC` | Nature 经典蓝，主结构、入口节点 |
| 强调 / 红 | `#D6604D` | Nature 经典红，终止、警告节点 |
| 成功 / 绿 | `#31A354` | Provider、成功路径 |
| 警告 / 橙 | `#E6550D` | DB、Registry、数据存储 |
| 紫色 | `#756BB1` | 配置、辅助协程 |
| 背景浅蓝 | `#EFF6FF` | 流程节点填充 |
| 背景浅红 | `#FFF5F0` | 终止/警告节点填充 |
| 中性灰 | `#757575` | 连线、次要文字 |

---

## 📊 Core Metrics

| | |
|---|---|
| **工具包 (pkg/\*)** | 70+ |
| **contrib 扩展模块** | 17 |
| **内置中间件** | 12 |
| **服务发现后端** | 5 (etcd / consul / nacos / polaris / k8s) |

---

## FIG. 1 — 整体架构总览

Beauty 核心极小（`beauty.New` → `App.Start`），通过 Option 函数式选项组合任意 Service，统一生命周期管理。

```mermaid
graph TB
    subgraph UserApp["① 用户业务层"]
        A1["业务 Handlers"]
        A2["领域逻辑 domain"]
        A3["自定义 Service"]
    end
    subgraph Core["② Beauty 核心"]
        B1["App 结构体"]
        B2["New + Options"]
        B3["Start 生命周期"]
        B4["startService 编排"]
    end
    subgraph Services["③ 内建服务"]
        C1["webserver"]
        C2["grpcserver"]
        C3["cron 定时"]
        C4["pprof 调试"]
    end
    subgraph Cross["④ 横切组件"]
        D1["config 热加载"]
        D2["telemetry Trace/Metric"]
        D3["logger"]
    end
    subgraph MW["⑤ 中间件链"]
        E1["accesslog → requestid → recovery<br/>ratelimit → auth → circuitbreaker → timeout"]
    end
    subgraph SD["⑥ 服务发现"]
        F1["Registry / Discovery 接口"]
        F2["etcd · consul · nacos · polaris · k8s"]
    end
    subgraph Client["⑦ 客户端"]
        G1["HTTP Client"]
        G2["gRPC Client"]
    end
    subgraph Utils["⑧ 工具库"]
        H1["cache · dlock · idgen · kvstore<br/>mq · eventbus · saga · txn<br/>ws · sse · quic · gameloop<br/>ratelimit · hedge · backoff · shard"]
    end
    subgraph Ext["⑨ contrib 扩展"]
        I1["gorm · sqldb · kafka · nats"]
        I2["llm · mcp · vector"]
        I3["wasm · wasmopa · wasmagent"]
        I4["casbin · openfga"]
    end
    UserApp -->|"Option 注入"| Core
    Core --> Services
    Core --> Cross
    Services --> MW --> A1
    Services -->|"Ready"| Core
    Core -->|"Register"| SD
    Client -->|"Find/Watch"| SD
    Cross -.->|"热更新/追踪"| Services
    A2 --> Utils
    A2 --> Ext
    Client -->|"LB 策略"| Services

    style B1 fill:#EFF6FF,color:#2166AC,stroke:#2166AC,stroke-width:1.5px
    style B2 fill:#EFF6FF,color:#2166AC,stroke:#2166AC,stroke-width:1.5px
    style B3 fill:#EFF6FF,color:#2166AC,stroke:#2166AC,stroke-width:1.5px
    style B4 fill:#EFF6FF,color:#2166AC,stroke:#2166AC,stroke-width:1.5px
    style F2 fill:#FFF5F0,color:#D6604D,stroke:#D6604D
```

---

## FIG. 2 — 启动与生命周期流程

每个 Service 启动时创建两个 goroutine：**编排协程**（就绪 → 注册 → 等待关停 → 注销 → drainDelay → stopServe）和 **serve 协程**（运行服务）。

```mermaid
flowchart TD
    START(["main() 入口"]) --> NEW["beauty.New(...)"]
    NEW --> LOOP{"遍历 Option"}
    LOOP -->|"WithWebServer"| O1["+ HTTP Service"]
    LOOP -->|"WithGrpcServer"| O2["+ gRPC Service"]
    LOOP -->|"WithCrontab"| O3["+ Cron Service"]
    LOOP -->|"WithService"| O4["+ 自定义 Service"]
    LOOP -->|"WithRegistry"| O5["+ Registry"]
    LOOP -->|"WithComponent"| O6["c.Init() + After hook"]
    O1 & O2 & O3 & O4 & O5 & O6 --> APP["返回 *App"]
    APP --> RUN["app.Start(ctx)"]
    RUN --> SIG["注册 SIGINT/SIGTERM"]
    SIG --> BEFORE["runHooks(BeforeRun)"]
    BEFORE --> READY["ready = 1"]
    READY --> FORK["每个 Service 调用 startService()"]
    FORK --> G1["🧵 编排协程<br/>等 Ready → Register → 等关停<br/>→ Deregister → drainDelay → stopServe"]
    FORK --> G2["🧵 serve 协程<br/>srv.Start(serveCtx)"]
    G1 --> STOP["收到关停信号"]
    G2 -->|"服务退出"| STOP
    STOP --> WAIT["svcWg.Wait()<br/>等所有服务退出"]
    WAIT --> AFTER["runHooks(AfterRun)<br/>停 Watch/Tracer/cleanup"]
    AFTER --> END(["logger.Sync() → 返回"])

    style START fill:#2166AC,color:#fff,stroke:#08519C,stroke-width:2px
    style END fill:#D6604D,color:#fff,stroke:#B2182B,stroke-width:2px
    style G1 fill:#3182BD,color:#fff,stroke:#08519C
    style G2 fill:#756BB1,color:#fff,stroke:#54278F
    style NEW fill:#EFF6FF,color:#2166AC,stroke:#2166AC
    style RUN fill:#EFF6FF,color:#2166AC,stroke:#2166AC
```

---

## FIG. 3 — HTTP 请求处理流程

洋葱模型中间件：请求从外到内穿过中间件链，响应从内到外逆序返回。OTel 追踪在最外层，业务逻辑在中心。

```mermaid
flowchart LR
    C["Client"] -->|"HTTP Request"| OT["otelhttp<br/>Trace Span"]
    OT --> M1["accesslog"]
    M1 --> M2["requestid"]
    M2 --> M3["recovery"]
    M3 --> M4["timeout"]
    M4 --> M5["ratelimit"]
    M5 --> M6["auth/authz"]
    M6 --> M7["circuitbreaker"]
    M7 --> MUX["ServeMux<br/>路由"]
    MUX --> H["业务 Handler"]
    H --> D["domain<br/>业务逻辑"]
    D -->|"远程调用"| CLI["HTTP/gRPC Client"]
    CLI --> DISC["服务发现 + LB"]
    DISC --> REM["远程实例"]
    D --> DB[("DB / Cache")]
    H -->|"Response 逆序"| C

    style C fill:#2166AC,color:#fff,stroke:#08519C,stroke-width:1.5px
    style DB fill:#E6550D,color:#fff,stroke:#A63603,stroke-width:1.5px
    style REM fill:#31A354,color:#fff,stroke:#006D2C,stroke-width:1.5px
    style D fill:#EFF6FF,color:#2166AC,stroke:#2166AC
    style OT fill:#FFF5F0,color:#D6604D,stroke:#D6604D
```

---

## FIG. 4 — 优雅停机时序

**先注销 → 排空等待 → 停服务**，确保 LB 感知下线后不再路由新请求，同时处理完 in-flight 请求，实现滚动发布零停机。

```mermaid
sequenceDiagram
    participant S as Signal
    participant App as App
    participant O as Orchestrator
    participant Sv as Service
    participant R as Registry
    participant L as LB/Client
    S->>App: SIGINT/SIGTERM
    App->>App: cancel(ctx)
    App->>O: ctx.Done()
    O->>R: deregister()
    R-->>L: instance offline
    Note over O,L: ⏳ drainDelay<br/>LB drains traffic
    O->>Sv: stopServe()
    Sv->>Sv: Shutdown(timeout)<br/>wait in-flight
    Sv-->>App: Start() returns
    App->>App: svcWg.Wait()
    App->>App: AfterRun hooks
```

---

## FIG. 5 — 服务发现与客户端调用

Server Listen 成功后 Register 到注册中心（keepalive 租约），Client Watch 端点列表本地缓存，调用时走负载均衡策略。

```mermaid
flowchart TD
    subgraph P["Provider"]
        p["webserver/grpcserver<br/>Listen → Ready()"]
    end
    subgraph R["Registry"]
        r["etcd/consul/nacos<br/>Register + keepalive"]
    end
    subgraph C["Consumer"]
        c1["Client.Start → Watch"]
        c2["endpoints cache"]
        c3["DoWith/Do call"]
    end
    subgraph L["Load Balance"]
        l["WRR / P2C / Hash<br/>filter + retry"]
    end
    p -->|"Register"| r
    c1 -->|"Watch"| r
    r -->|"Notify"| c2
    c3 --> c2 --> l -->|"invoke"| p

    style p fill:#31A354,color:#fff,stroke:#006D2C,stroke-width:1.5px
    style r fill:#E6550D,color:#fff,stroke:#A63603,stroke-width:1.5px
    style c3 fill:#2166AC,color:#fff,stroke:#08519C,stroke-width:1.5px
```

---

## FIG. 6 — 配置热更新流程

**先校验后提交**：conf.Loader Watch 配置源变更，先 Unmarshal 校验再提交；坏配置不会覆盖上一份可用值。

```mermaid
flowchart LR
    S["配置源<br/>file / etcd / nacos<br/>consul / k8s"] -->|"Watch"| L["conf.Loader"]
    L -->|"callback"| CC["configComponent"]
    CC --> U["Unmarshal"]
    U --> V{"valid?"}
    V -->|"yes"| CB["onChange(cfg)"]
    CB --> AT["atomic.Pointer<br/>thread-safe swap"]
    AT --> B["biz reads cfg"]
    V -->|"no"| E["log error<br/>keep old cfg"]

    style S fill:#756BB1,color:#fff,stroke:#54278F,stroke-width:1.5px
    style B fill:#31A354,color:#fff,stroke:#006D2C,stroke-width:1.5px
    style E fill:#D6604D,color:#fff,stroke:#B2182B,stroke-width:1.5px
    style V fill:#FFF5F0,color:#D6604D,stroke:#D6604D
```

---

## FIG. 7 — 分层依赖关系

七层架构，依赖方向**自上而下**，核心极小 —— **机制而非策略**，用什么才引什么，按需组装。

```mermaid
graph TB
    L1["① User App<br/>main.go"] --> L2["② Beauty API<br/>beauty.go · service.go"]
    L2 --> L3["③ Built-in Services<br/>webserver · grpc · cron"]
    L2 --> L4["④ Middleware<br/>auth · ratelimit · recovery"]
    L2 --> L5["⑤ Clients<br/>http · grpcclient"]
    L3 --> L4
    L3 --> L6["⑥ Utilities (70+)<br/>cache · mq · ws · saga"]
    L4 --> L6
    L5 --> L6
    L1 --> L6
    L1 --> L7["⑦ contrib Extensions<br/>gorm · kafka · llm · wasm"]
    L6 --> L7

    style L1 fill:#D6604D,color:#fff,stroke:#B2182B,stroke-width:1.5px
    style L2 fill:#2166AC,color:#fff,stroke:#08519C,stroke-width:1.5px
    style L7 fill:#31A354,color:#fff,stroke:#006D2C,stroke-width:1.5px
```

---

## 📁 Files

| 文件 | 说明 |
|------|------|
| [`architecture.html`](architecture.html) | 发表级配色 HTML 可视化（推荐浏览器打开） |
| [`architecture-diagram.md`](architecture-diagram.md) | Markdown 版本，可在 GitHub/IDE 中直接渲染 |
