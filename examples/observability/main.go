// 可观测性开箱即用示例：HTTP + gRPC 服务把 trace / metric 通过 OTLP 推到 OpenTelemetry
// Collector，再分发到 Tempo(trace) 与 Prometheus(metric)，Grafana 预置数据源与看板。
//
//	cd examples/observability
//	docker compose up -d          # Collector / Tempo / Prometheus / Grafana
//	go run .                      # 启动 demo 服务(内置流量发生器)
//	open http://localhost:3000    # Grafana → Dashboards → Beauty 服务总览
//
// 覆盖的信号：
//   - HTTP RED：webserver 内置 otelhttp，产出 http.server.request.duration；
//   - gRPC RED：grpcserver 内置 otelgrpc，/api/orders/{id} 通过 gRPC 调用本进程 health 服务，
//     trace 在 HTTP → gRPC 之间串联；
//   - 熔断器状态：pkg/resilience/circuitbreaker 的状态经 ObservableGauge 暴露；
//   - 业务指标：自定义 counter；
//   - Go runtime：telemetry.WithMetric 默认开启；
//   - 日志：slog 经 logger.NewTraceHandler 自动带 trace_id/span_id，可与 Tempo 中的 trace 对照。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"time"

	"github.com/rushteam/beauty"
	"github.com/rushteam/beauty/pkg/middleware/accesslog"
	"github.com/rushteam/beauty/pkg/resilience/circuitbreaker"
	"github.com/rushteam/beauty/pkg/service/grpcserver"
	"github.com/rushteam/beauty/pkg/service/logger"
	"github.com/rushteam/beauty/pkg/service/telemetry"
	"github.com/rushteam/beauty/pkg/service/webserver"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

const serviceName = "obs-demo"

var (
	httpAddr = flag.String("http", ":8080", "HTTP 监听地址")
	grpcAddr = flag.String("grpc", ":9090", "gRPC 监听地址")
	otlp     = flag.String("otlp", envOr("OTLP_ENDPOINT", "localhost:4317"), "OTLP/gRPC Collector 地址")
	load     = flag.Bool("load", true, "启动内置流量发生器")
)

func main() {
	flag.Parse()
	slog.SetDefault(slog.New(logger.NewTraceHandler(slog.NewJSONHandler(os.Stdout, nil))))

	metricExporter, err := otlpmetricgrpc.New(context.Background(),
		otlpmetricgrpc.WithEndpoint(*otlp), otlpmetricgrpc.WithInsecure())
	if err != nil {
		slog.Error("创建 metric exporter 失败", "err", err)
		os.Exit(1)
	}

	d, err := newDemo(*grpcAddr)
	if err != nil {
		slog.Error("初始化失败", "err", err)
		os.Exit(1)
	}

	resAttrs := []attribute.KeyValue{semconv.ServiceVersion("v1.0.0"), attribute.String("deployment.environment.name", "dev")}
	opts := []beauty.Option{
		beauty.WithTrace(
			telemetry.WithTraceServiceName(serviceName, resAttrs...),
			telemetry.WithTraceOTLPGRPCExporter(otlptracegrpc.WithEndpoint(*otlp), otlptracegrpc.WithInsecure()),
		),
		beauty.WithMetric(
			telemetry.WithMetricServiceName(serviceName, resAttrs...),
			// 看板按 1m 窗口计算速率，SDK 默认 60s 的上报间隔太稀疏
			telemetry.WithMetricReader(sdkmetric.NewPeriodicReader(metricExporter, sdkmetric.WithInterval(10*time.Second))),
		),
		beauty.WithWebServer(*httpAddr, d.routes(),
			webserver.WithServiceName(serviceName),
			webserver.WithMiddleware(accesslog.HTTPMiddleware),
		),
		beauty.WithGrpcServer(*grpcAddr, func(*grpc.Server) {}, grpcserver.WithServiceName(serviceName)),
	}
	if *load {
		opts = append(opts, beauty.WithService(&loadGenerator{target: "http://localhost" + *httpAddr}))
	}
	app := beauty.New(opts...)

	slog.Info("obs-demo 启动", "http", *httpAddr, "grpc", *grpcAddr, "otlp", *otlp)
	if err := app.Start(context.Background()); err != nil {
		slog.Error("退出", "err", err)
	}
}

type demo struct {
	tracer  trace.Tracer
	orders  metric.Int64Counter
	breaker *circuitbreaker.Breaker
	health  healthpb.HealthClient
}

func newDemo(grpcAddr string) (*demo, error) {
	meter := telemetry.Meter()
	orders, err := meter.Int64Counter("demo.orders", metric.WithDescription("已处理订单数"), metric.WithUnit("{order}"))
	if err != nil {
		return nil, err
	}

	breaker := circuitbreaker.New(
		circuitbreaker.WithThreshold(0.5),
		circuitbreaker.WithMinRequests(10),
		circuitbreaker.WithWindow(10*time.Second),
		circuitbreaker.WithCooldown(5*time.Second),
		circuitbreaker.WithOnStateChange(func(from, to circuitbreaker.State) {
			slog.Warn("熔断器状态变化", "breaker", "payment", "from", from.String(), "to", to.String())
		}),
	)
	// 0=closed 1=open 2=half-open，与 circuitbreaker.State 取值一致
	_, err = meter.Int64ObservableGauge("beauty.circuitbreaker.state",
		metric.WithDescription("熔断器状态: 0=closed 1=open 2=half-open"),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			o.Observe(int64(breaker.State()), metric.WithAttributes(attribute.String("breaker", "payment")))
			return nil
		}),
	)
	if err != nil {
		return nil, err
	}

	conn, err := grpc.NewClient("localhost"+grpcAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		return nil, err
	}

	return &demo{
		tracer:  otel.Tracer(serviceName),
		orders:  orders,
		breaker: breaker,
		health:  healthpb.NewHealthClient(conn),
	}, nil
}

func (d *demo) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/orders/{id}", d.getOrder)
	mux.HandleFunc("POST /api/pay", d.pay)
	mux.HandleFunc("GET /api/slow", d.slow)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok") })
	return mux
}

// getOrder 演示一条跨 HTTP → 本地子 span → gRPC 的完整 trace。
func (d *demo) getOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")

	func() {
		_, span := d.tracer.Start(ctx, "db.query order", trace.WithAttributes(attribute.String("order.id", id)))
		defer span.End()
		time.Sleep(jitter(5*time.Millisecond, 30*time.Millisecond))
	}()

	if _, err := d.health.Check(ctx, &healthpb.HealthCheckRequest{}); err != nil {
		slog.ErrorContext(ctx, "库存服务调用失败", "err", err)
		http.Error(w, "inventory unavailable", http.StatusBadGateway)
		return
	}

	if rand.IntN(100) < 3 {
		slog.ErrorContext(ctx, "订单不存在", "order_id", id)
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	d.orders.Add(ctx, 1, metric.WithAttributes(attribute.String("result", "ok")))
	fmt.Fprintf(w, `{"id":%q,"status":"paid"}`, id)
}

var errPaymentDown = errors.New("payment gateway timeout")

// pay 调用一个会周期性"故障"的下游，由熔断器保护：故障期间熔断器打开，快速失败返回 503。
func (d *demo) pay(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	err := d.breaker.Do(func() error {
		_, span := d.tracer.Start(ctx, "payment.charge")
		defer span.End()
		time.Sleep(jitter(10*time.Millisecond, 50*time.Millisecond))
		if paymentDegraded() && rand.IntN(100) < 80 {
			span.RecordError(errPaymentDown)
			return errPaymentDown
		}
		return nil
	})
	switch {
	case errors.Is(err, circuitbreaker.ErrCircuitOpen):
		http.Error(w, "circuit open", http.StatusServiceUnavailable)
	case err != nil:
		slog.ErrorContext(ctx, "支付失败", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	default:
		io.WriteString(w, `{"paid":true}`)
	}
}

// slow 产生长尾延迟，便于在延迟直方图上通过 exemplar 跳转到对应 trace。
func (d *demo) slow(w http.ResponseWriter, r *http.Request) {
	delay := jitter(50*time.Millisecond, 150*time.Millisecond)
	if rand.IntN(100) < 5 {
		delay = jitter(800*time.Millisecond, 1500*time.Millisecond)
	}
	time.Sleep(delay)
	fmt.Fprintf(w, `{"delay_ms":%d}`, delay.Milliseconds())
}

// paymentDegraded 每 2 分钟里有 30 秒处于故障期，用来触发熔断器开合。
func paymentDegraded() bool {
	return time.Now().Unix()%120 < 30
}

func jitter(lo, hi time.Duration) time.Duration {
	return lo + rand.N(hi-lo)
}

// loadGenerator 是一个 beauty.Service：持续向本服务发请求，随 App 一起启停。
type loadGenerator struct {
	target string
}

func (g *loadGenerator) String() string { return "load-generator" }

func (g *loadGenerator) Start(ctx context.Context) error {
	client := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport), Timeout: 5 * time.Second}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		method, path := http.MethodGet, fmt.Sprintf("/api/orders/%d", rand.IntN(1000))
		switch n := rand.IntN(10); {
		case n < 3:
			method, path = http.MethodPost, "/api/pay"
		case n < 5:
			path = "/api/slow"
		}
		req, err := http.NewRequestWithContext(ctx, method, g.target+path, nil)
		if err != nil {
			return err
		}
		if resp, err := client.Do(req); err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
