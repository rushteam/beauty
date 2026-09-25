# 可观测性开箱即用

一条命令拉起 **OpenTelemetry Collector + Tempo + Prometheus + Grafana**，配合一个自带流量发生器的
demo 服务，打开 Grafana 就能看到预置的「Beauty 服务总览」看板：HTTP / gRPC 的 RED 指标、熔断器状态、
业务计数、Go runtime，以及从延迟图一键跳转到 trace。

```text
 obs-demo (beauty)                       ┌──────────┐
  ├─ webserver  (otelhttp)  ─┐           │  Tempo   │◀── traces ──┐
  ├─ grpcserver (otelgrpc)  ─┼─ OTLP ──▶ │          │             │
  ├─ 自定义 metric / span    ─┘  :4317    └──────────┘     ┌───────┴────────┐
  └─ load-generator                                       │ otel-collector │
                                          ┌────────────┐  └───────┬────────┘
                                          │ Prometheus │◀─ scrape :8889 (metrics + exemplars)
                                          └─────┬──────┘
                                                ▼
                                          ┌──────────┐
                                          │ Grafana  │  :3000(匿名 Admin)
                                          └──────────┘
```

## 运行

```bash
cd examples/observability
docker compose up -d        # 启动观测栈
go run .                    # 启动 demo(HTTP :8080，gRPC :9090，内置压测流量)
```

打开 <http://localhost:3000> → Dashboards → Beauty → **Beauty 服务总览**。约 30 秒后开始有数据。

| 地址 | 用途 |
|---|---|
| <http://localhost:3000> | Grafana(看板 / Explore / Tempo 搜索) |
| <http://localhost:9091> | Prometheus |
| <http://localhost:3200> | Tempo API |
| `localhost:4317` / `4318` | Collector OTLP gRPC / HTTP 入口 |

清理：`docker compose down`。

### demo 参数

| 参数 | 默认 | 说明 |
|---|---|---|
| `-http` | `:8080` | HTTP 监听地址 |
| `-grpc` | `:9090` | gRPC 监听地址 |
| `-otlp` | `localhost:4317`(或环境变量 `OTLP_ENDPOINT`) | Collector 地址 |
| `-load` | `true` | 是否启动内置流量发生器；设为 `false` 后可自己 `curl` |

## 看点

- **HTTP RED**：`/api/orders/{id}`、`/api/pay`、`/api/slow` 三条路由的速率、4xx/5xx、P50/P95/P99。
  `webserver` 内置 otelhttp，无需额外代码。
- **exemplar → trace**：延迟面板开启了 exemplar，图上的小点点开即可跳到 Tempo 中对应的 trace。
  `/api/slow` 有 5% 的长尾请求，最适合用来体验。
- **跨协议 trace**：`/api/orders/{id}` 内部经 gRPC 调用本进程的 health 服务，trace 里能看到
  HTTP server → 本地 `db.query order` span → gRPC client → gRPC server 的完整链路。
- **熔断器**：`/api/pay` 的下游每 2 分钟有 30 秒进入故障期，`pkg/resilience/circuitbreaker`
  随之 closed → open → half-open → closed，状态时间线面板里能直观看到；熔断打开期间请求快速返回 503。
- **服务拓扑**：Tempo 的 metrics-generator 从 trace 派生 service graph，在 Grafana Explore →
  Tempo → Service Graph 中查看。
- **日志关联**：demo 用 `logger.NewTraceHandler` 包装 slog，每条 JSON 日志自动带 `trace_id` / `span_id`，
  可复制到 Tempo 里检索。

## 指标名对照

OTel 指标经 Collector 的 Prometheus exporter 转换后：`.` 变 `_`，追加单位与 `_total` 后缀，
`service.name` 变为 `job` 标签。

| OTel 指标 | Prometheus 指标 | 来源 |
|---|---|---|
| `http.server.request.duration` (s) | `http_server_request_duration_seconds_*` | webserver(otelhttp) |
| `rpc.server.duration` (ms) | `rpc_server_duration_milliseconds_*` | grpcserver(otelgrpc) |
| `go.goroutine.count` | `go_goroutine_count` | `telemetry.WithMetric` 默认开启的 runtime 指标 |
| `go.memory.used` (By) | `go_memory_used_bytes` | 同上 |
| `beauty.circuitbreaker.state` | `beauty_circuitbreaker_state` | 本示例的 ObservableGauge |
| `demo.orders` | `demo_orders_total` | 本示例的 Counter |

## 用到自己的服务

看板只依赖 `job` 标签(= `service.name`)与上表的标准指标，任何用 `webserver` / `grpcserver`
的 beauty 服务按下面接入即可直接复用：

```go
app := beauty.New(
    beauty.WithTrace(
        telemetry.WithTraceServiceName("order-api"),
        telemetry.WithTraceOTLPGRPCExporter(), // 读取标准 OTEL_EXPORTER_OTLP_* 环境变量
    ),
    beauty.WithMetric(
        telemetry.WithMetricServiceName("order-api"),
        telemetry.WithMetricOTLPGRPCReader(),
    ),
    beauty.WithWebServer(":8080", mux),
)
```

```bash
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317 OTEL_EXPORTER_OTLP_INSECURE=true go run .
```

`deploy/grafana/dashboards/beauty-overview.json` 可直接导入已有的 Grafana(数据源 uid 为
`prometheus` / `tempo`，不同时在导入时替换)。LLM 调用的 token / 延迟指标见
[`contrib/otelllm`](../../contrib/otelllm)。
