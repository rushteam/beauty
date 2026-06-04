package telemetry

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"

	"github.com/rushteam/beauty/pkg/service/core"

	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

var meter metric.Meter

type MetricOption func(c *metricComponent)

func WithMetricReader(reader sdkmetric.Reader) MetricOption {
	return func(o *metricComponent) {
		o.options = append(o.options, sdkmetric.WithReader(reader))
	}
}

func WithMetricOption(opts ...sdkmetric.Option) MetricOption {
	return func(o *metricComponent) {
		o.options = append(o.options, opts...)
	}
}

func WithMetricProvider(provider metric.MeterProvider) MetricOption {
	return func(o *metricComponent) {
		o.provider = provider
	}
}

// WithMetricRuntime 启用 Go runtime 指标采集（goroutine 数、GC 次数/暂停、heap 大小等），
// 由 OTel contrib 的 runtime instrumentation 提供。默认已开启，调用此方法可传入自定义选项。
//
//	// 调整 MemStats 最小读取间隔（默认 15s）
//	WithMetricRuntime(runtime.WithMinimumReadMemStatsInterval(30 * time.Second))
func WithMetricRuntime(opts ...runtime.Option) MetricOption {
	return func(o *metricComponent) {
		o.runtime = true
		o.runtimeOptions = append(o.runtimeOptions, opts...)
	}
}

// WithoutMetricRuntime 关闭 Go runtime 指标采集。
func WithoutMetricRuntime() MetricOption {
	return func(o *metricComponent) {
		o.runtime = false
	}
}

// WithMetricOTLPGRPCReader 通过 OTLP/gRPC（默认端口 4317）周期性上报指标到 Collector。
// 指标为推送模型，exporter 包在 PeriodicReader 内（默认 60s 间隔）。
// 不传 opts 时自动读取标准 OTEL_EXPORTER_OTLP_* 环境变量；也可显式配置：
//
//	WithMetricOTLPGRPCReader(
//		otlpmetricgrpc.WithEndpoint("otel-collector:4317"),
//		otlpmetricgrpc.WithInsecure(),
//	)
//
// 需要自定义上报间隔时，改用 WithMetricReader(sdkmetric.NewPeriodicReader(exporter, ...))。
func WithMetricOTLPGRPCReader(opts ...otlpmetricgrpc.Option) MetricOption {
	exporter, err := otlpmetricgrpc.New(context.Background(), opts...)
	if err != nil {
		panic(fmt.Sprintf("telemetry: failed to create OTLP/gRPC metric exporter: %v", err))
	}
	return WithMetricReader(sdkmetric.NewPeriodicReader(exporter))
}

// WithMetricOTLPHTTPReader 通过 OTLP/HTTP（默认端口 4318）周期性上报指标。
// 不传 opts 时自动读取标准 OTEL_EXPORTER_OTLP_* 环境变量；也可显式配置：
//
//	WithMetricOTLPHTTPReader(
//		otlpmetrichttp.WithEndpoint("otel-collector:4318"),
//		otlpmetrichttp.WithInsecure(),
//	)
func WithMetricOTLPHTTPReader(opts ...otlpmetrichttp.Option) MetricOption {
	exporter, err := otlpmetrichttp.New(context.Background(), opts...)
	if err != nil {
		panic(fmt.Sprintf("telemetry: failed to create OTLP/HTTP metric exporter: %v", err))
	}
	return WithMetricReader(sdkmetric.NewPeriodicReader(exporter))
}

func WithMetricStdoutReader() MetricOption {
	exporter, err := stdoutmetric.New(
		stdoutmetric.WithPrettyPrint(),
	)
	if err != nil {
		panic(fmt.Sprintf("telemetry: failed to create stdout metric exporter: %v", err))
	}
	return WithMetricReader(sdkmetric.NewPeriodicReader(
		exporter,
		sdkmetric.WithInterval(5*time.Second), // default 1m, for test 5s
	))
}

type metricComponent struct {
	provider       metric.MeterProvider
	options        []sdkmetric.Option
	runtime        bool
	runtimeOptions []runtime.Option
}

func (c *metricComponent) Name() string {
	return "metric"
}

func (c *metricComponent) Init() context.CancelFunc {
	setupErrorHandler() // 把异步导出错误接到项目 logger，否则会被默默打到 stderr
	if c.provider == nil {
		c.provider = sdkmetric.NewMeterProvider(c.options...)
	}
	otel.SetMeterProvider(c.provider)

	// 启用 Go runtime 指标（goroutine 数、GC、heap 等）。无 reader 时 provider 会丢弃数据，零成本。
	if c.runtime {
		opts := append([]runtime.Option{runtime.WithMeterProvider(c.provider)}, c.runtimeOptions...)
		if err := runtime.Start(opts...); err != nil {
			panic(fmt.Sprintf("telemetry: failed to start runtime metrics: %v", err))
		}
	}

	return func() {
		type shutdown interface {
			Shutdown(ctx context.Context) error
		}
		if p, ok := c.provider.(shutdown); ok {
			_ = p.Shutdown(context.Background())
		}
	}
}

func NewMetric(opts ...MetricOption) core.Component {
	c := &metricComponent{runtime: true} // 默认开启 Go runtime 指标，可用 WithoutMetricRuntime 关闭
	// if len(opts) == 0 {
	// 	opts = append(opts, WithMetricStdoutReader())
	// }
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func Meter() metric.Meter {
	if meter == nil {
		meter = otel.GetMeterProvider().Meter("beauty")
	}
	return meter
}
