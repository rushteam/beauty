package telemetry

import (
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/exemplar"
)

// WithMetricExemplarFilter 应把 exemplar filter 选项追加到 provider 配置，
// 且组件能正常 Init / 关闭（不 panic）。
func TestWithMetricExemplarFilter(t *testing.T) {
	c := &metricComponent{}
	WithMetricExemplarFilter(exemplar.AlwaysOffFilter)(c)
	if len(c.options) != 1 {
		t.Fatalf("expected 1 metric option appended, got %d", len(c.options))
	}

	comp := NewMetric(
		WithoutMetricRuntime(), // 避免后台 runtime 采集干扰测试
		WithMetricExemplarFilter(exemplar.AlwaysOffFilter),
	)
	cancel := comp.Init()
	t.Cleanup(cancel)
}

// 默认（不传 filter）也应能正常构建 provider —— SDK 默认即 trace_based。
func TestMetricDefaultExemplar(t *testing.T) {
	comp := NewMetric(WithoutMetricRuntime())
	cancel := comp.Init()
	t.Cleanup(cancel)

	if comp.(*metricComponent).provider == nil {
		t.Fatal("provider should be initialized")
	}
}

var _ sdkmetric.Option = sdkmetric.WithExemplarFilter(exemplar.TraceBasedFilter)
