// Package media 提供直播/视频服务的编排薄机制:多路流管理(Hub)、子进程监督
// (Supervisor,如 ffmpeg 转码进程,含崩溃重启)与 OTel 运维指标。
//
// 边界(与框架"薄机制"一致):只做"管理与编排"这层通用苦活——注册表、生命周期、
// 路由、进程重启、指标埋点。**不做 policy**:每路流怎么建(hls.Stream 配置)、跑什么
// 命令(ffmpeg 参数)、转码档位、导出到哪(Prometheus/OTLP),全由调用方决定。
package media

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Metrics 是一组直播运维指标,基于 OTel 全局 MeterProvider(由 pkg/service/telemetry
// 配置)。未配置 telemetry 时全部为 no-op,零开销。按 stream 标签维度上报。
type Metrics struct {
	active    metric.Int64UpDownCounter
	publish   metric.Int64Counter
	unpublish metric.Int64Counter
	rejected  metric.Int64Counter
	ingest    metric.Int64Counter
	segments  metric.Int64Counter
	restarts  metric.Int64Counter
}

// NewMetrics 从全局 Meter 创建指标(instrument 创建失败时对应项为 nil,记录时跳过)。
func NewMetrics() *Metrics {
	m := otel.Meter("github.com/rushteam/beauty/pkg/media")
	mk := func(name, desc string) metric.Int64Counter {
		c, _ := m.Int64Counter(name, metric.WithDescription(desc))
		return c
	}
	active, _ := m.Int64UpDownCounter("media.streams.active", metric.WithDescription("当前在线流数"))
	return &Metrics{
		active:    active,
		publish:   mk("media.publish.total", "推流开始次数"),
		unpublish: mk("media.unpublish.total", "推流结束次数"),
		rejected:  mk("media.publish.rejected", "被拒绝的推流次数"),
		ingest:    mk("media.ingest.bytes", "采集入流量(字节)"),
		segments:  mk("media.segments.total", "产出分片数"),
		restarts:  mk("media.transcode.restarts", "转码进程重启次数"),
	}
}

func streamAttr(key string) metric.MeasurementOption {
	return metric.WithAttributes(attribute.String("stream", key))
}

func (m *Metrics) incActive(ctx context.Context, key string, delta int64) {
	if m != nil && m.active != nil {
		m.active.Add(ctx, delta, streamAttr(key))
	}
}

func (m *Metrics) add(ctx context.Context, c metric.Int64Counter, key string, n int64) {
	if c != nil {
		c.Add(ctx, n, streamAttr(key))
	}
}

// IngestBytes 上报某路流采集入流量(由调用方在收到音视频时累加)。
func (m *Metrics) IngestBytes(ctx context.Context, key string, n int64) {
	if m != nil {
		m.add(ctx, m.ingest, key, n)
	}
}

// Segment 上报某路流产出一个分片(由调用方在 Append/切片时调用)。
func (m *Metrics) Segment(ctx context.Context, key string) {
	if m != nil {
		m.add(ctx, m.segments, key, 1)
	}
}

// Restart 上报某路流的转码进程重启(Supervisor 内部调用)。
func (m *Metrics) Restart(ctx context.Context, key string) {
	if m != nil {
		m.add(ctx, m.restarts, key, 1)
	}
}
