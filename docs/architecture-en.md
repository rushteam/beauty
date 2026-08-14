# Beauty Framework — Architecture

> **One lifecycle runs microservices — HTTP · gRPC · Cron · Real-time Media · WASM Agent Sandbox**

---

## Publication-Quality Color Palette (Nature/Science)

| Purpose | Color | Description |
|------|------|------|
| Primary / Blue | `#2166AC` | Nature classic blue — main structure, entry nodes |
| Accent / Red | `#D6604D` | Nature classic red — termination, warning nodes |
| Success / Green | `#31A354` | Provider, success paths |
| Warning / Orange | `#E6550D` | DB, Registry, data storage |
| Purple | `#756BB1` | Configuration, auxiliary goroutines |
| Light Blue Background | `#EFF6FF` | Flow node fill |
| Light Red Background | `#FFF5F0` | Termination/warning node fill |
| Neutral Gray | `#757575` | Connectors, secondary text |

---

## Core Metrics

| | |
|---|---|
| **Utility Packages (pkg/\*)** | 70+ |
| **contrib Extension Modules** | 17 |
| **Built-in Middleware** | 12 |
| **Service Discovery Backends** | 5 (etcd / consul / nacos / polaris / k8s) |

---

## FIG. 1 — Overall Architecture Overview

Beauty's core is minimal (`beauty.New` → `App.Start`). Option functional options compose any number of Services under unified lifecycle management.

```mermaid
graph TB
    subgraph UserApp["① User Application Layer"]
        A1["Business Handlers"]
        A2["Domain Logic"]
        A3["Custom Service"]
    end
    subgraph Core["② Beauty Core"]
        B1["App struct"]
        B2["New + Options"]
        B3["Start lifecycle"]
        B4["startService orchestration"]
    end
    subgraph Services["③ Built-in Services"]
        C1["webserver"]
        C2["grpcserver"]
        C3["cron"]
        C4["pprof debug"]
    end
    subgraph Cross["④ Cross-cutting Components"]
        D1["config hot reload"]
        D2["telemetry Trace/Metric"]
        D3["logger"]
    end
    subgraph MW["⑤ Middleware Chain"]
        E1["accesslog → requestid → recovery<br/>ratelimit → auth → circuitbreaker → timeout"]
    end
    subgraph SD["⑥ Service Discovery"]
        F1["Registry / Discovery interfaces"]
        F2["etcd · consul · nacos · polaris · k8s"]
    end
    subgraph Client["⑦ Clients"]
        G1["HTTP Client"]
        G2["gRPC Client"]
    end
    subgraph Utils["⑧ Utility Libraries"]
        H1["cache · dlock · idgen · kvstore<br/>mq · eventbus · saga · txn<br/>ws · sse · quic · gameloop<br/>ratelimit · hedge · backoff · shard"]
    end
    subgraph Ext["⑨ contrib Extensions"]
        I1["gorm · sqldb · kafka · nats"]
        I2["llm · mcp · vector"]
        I3["wasm · wasmopa · wasmagent"]
        I4["casbin · openfga"]
    end
    UserApp -->|"Option injection"| Core
    Core --> Services
    Core --> Cross
    Services --> MW --> A1
    Services -->|"Ready"| Core
    Core -->|"Register"| SD
    Client -->|"Find/Watch"| SD
    Cross -.->|"Hot reload / tracing"| Services
    A2 --> Utils
    A2 --> Ext
    Client -->|"LB strategy"| Services

    style B1 fill:#EFF6FF,color:#2166AC,stroke:#2166AC,stroke-width:1.5px
    style B2 fill:#EFF6FF,color:#2166AC,stroke:#2166AC,stroke-width:1.5px
    style B3 fill:#EFF6FF,color:#2166AC,stroke:#2166AC,stroke-width:1.5px
    style B4 fill:#EFF6FF,color:#2166AC,stroke:#2166AC,stroke-width:1.5px
    style F2 fill:#FFF5F0,color:#D6604D,stroke:#D6604D
```

---

## FIG. 2 — Startup and Lifecycle Flow

Each Service spawns two goroutines on startup: an **orchestration goroutine** (ready → register → wait for shutdown → deregister → drainDelay → stopServe) and a **serve goroutine** (runs the service).

```mermaid
flowchart TD
    START(["main() entry"]) --> NEW["beauty.New(...)"]
    NEW --> LOOP{"Iterate Options"}
    LOOP -->|"WithWebServer"| O1["+ HTTP Service"]
    LOOP -->|"WithGrpcServer"| O2["+ gRPC Service"]
    LOOP -->|"WithCrontab"| O3["+ Cron Service"]
    LOOP -->|"WithService"| O4["+ Custom Service"]
    LOOP -->|"WithRegistry"| O5["+ Registry"]
    LOOP -->|"WithComponent"| O6["c.Init() + After hook"]
    O1 & O2 & O3 & O4 & O5 & O6 --> APP["Return *App"]
    APP --> RUN["app.Start(ctx)"]
    RUN --> SIG["Register SIGINT/SIGTERM"]
    SIG --> BEFORE["runHooks(BeforeRun)"]
    BEFORE --> READY["ready = 1"]
    READY --> FORK["Each Service calls startService()"]
    FORK --> G1["Orchestration goroutine<br/>Wait Ready → Register → Wait shutdown<br/>→ Deregister → drainDelay → stopServe"]
    FORK --> G2["Serve goroutine<br/>srv.Start(serveCtx)"]
    G1 --> STOP["Receive shutdown signal"]
    G2 -->|"Service exits"| STOP
    STOP --> WAIT["svcWg.Wait()<br/>Wait for all services to exit"]
    WAIT --> AFTER["runHooks(AfterRun)<br/>Stop Watch/Tracer/cleanup"]
    AFTER --> END(["logger.Sync() → return"])

    style START fill:#2166AC,color:#fff,stroke:#08519C,stroke-width:2px
    style END fill:#D6604D,color:#fff,stroke:#B2182B,stroke-width:2px
    style G1 fill:#3182BD,color:#fff,stroke:#08519C
    style G2 fill:#756BB1,color:#fff,stroke:#54278F
    style NEW fill:#EFF6FF,color:#2166AC,stroke:#2166AC
    style RUN fill:#EFF6FF,color:#2166AC,stroke:#2166AC
```

---

## FIG. 3 — HTTP Request Processing Flow

Onion-model middleware: requests pass through the middleware chain from outside in; responses return in reverse order. OTel tracing is outermost; business logic sits at the center.

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
    M7 --> MUX["ServeMux<br/>routing"]
    MUX --> H["Business Handler"]
    H --> D["domain<br/>business logic"]
    D -->|"Remote call"| CLI["HTTP/gRPC Client"]
    CLI --> DISC["Service discovery + LB"]
    DISC --> REM["Remote instance"]
    D --> DB[("DB / Cache")]
    H -->|"Response (reverse order)"| C

    style C fill:#2166AC,color:#fff,stroke:#08519C,stroke-width:1.5px
    style DB fill:#E6550D,color:#fff,stroke:#A63603,stroke-width:1.5px
    style REM fill:#31A354,color:#fff,stroke:#006D2C,stroke-width:1.5px
    style D fill:#EFF6FF,color:#2166AC,stroke:#2166AC
    style OT fill:#FFF5F0,color:#D6604D,stroke:#D6604D
```

---

## FIG. 4 — Graceful Shutdown Sequence

**Deregister first → drain wait → stop service** ensures the load balancer stops routing new requests after the instance goes offline, while in-flight requests complete—enabling zero-downtime rolling deployments.

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
    Note over O,L: drainDelay<br/>LB drains traffic
    O->>Sv: stopServe()
    Sv->>Sv: Shutdown(timeout)<br/>wait in-flight
    Sv-->>App: Start() returns
    App->>App: svcWg.Wait()
    App->>App: AfterRun hooks
```

---

## FIG. 5 — Service Discovery and Client Calls

After the server listens successfully, it registers with the registry (keepalive lease). The client watches the endpoint list and caches it locally; calls use load balancing strategies.

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

## FIG. 6 — Configuration Hot Reload Flow

**Validate before commit**: `conf.Loader` watches the configuration source for changes, unmarshals and validates first, then commits; invalid configuration does not overwrite the last known good value.

```mermaid
flowchart LR
    S["Config source<br/>file / etcd / nacos<br/>consul / k8s"] -->|"Watch"| L["conf.Loader"]
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

## FIG. 7 — Layered Dependency Relationships

Seven-layer architecture with dependencies flowing **top-down**. The core is minimal — **mechanisms, not policy**; import only what you use and assemble as needed.

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

## Files

| File | Description |
|------|------|
| [`architecture.html`](architecture.html) | Publication-quality HTML visualization (recommended in browser) |
| [`architecture-diagram.md`](architecture-diagram.md) | Markdown version, renders directly in GitHub/IDE |
