package cron

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const (
	// ScopeName is the instrumentation scope name.
	ScopeName = "github.com/rushteam/beauty/pkg/service/cron/trace.go"
)

// getCallerShortInfo 获取调用者的文件名和行号
func getCallerShortInfo(skip int) string {
	_, file, line, ok := runtime.Caller(skip)
	if !ok {
		return "unknown"
	}

	// 分割路径
	parts := strings.Split(file, "/")

	// 如果路径段数小于2，直接返回完整文件名
	if len(parts) <= 2 {
		return fmt.Sprintf("%s:%d", file, line)
	}

	// 获取最后两级
	shortFile := strings.Join(parts[len(parts)-2:], "/")
	return fmt.Sprintf("%s:%d", shortFile, line)
}

func wrapCronHandler(cron *Cron, name string, spec string, handler func(ctx context.Context) error) func(ctx context.Context) error {
	metricAttrs := []attribute.KeyValue{
		{
			Key:   "cron_spec",
			Value: attribute.StringValue(spec),
		},
		{
			Key:   "name",
			Value: attribute.StringValue(name),
		},
	}
	metricsAttributeSetOpt := metric.WithAttributeSet(attribute.NewSet(metricAttrs...))
	traceName := "[cron] " + name // 拼一下，不然不好分辨
	return func(ctx context.Context) error {
		ctx, span := cron.tracer.Start(
			trace.ContextWithRemoteSpanContext(ctx, trace.SpanContextFromContext(ctx)),
			traceName,
			trace.WithSpanKind(trace.SpanKindInternal),
			trace.WithAttributes(attribute.String("name", name)),
			trace.WithAttributes(attribute.String("cron_spec", spec)),
		)
		defer func() {
			span.End()
			readOnlySpan := span.(sdktrace.ReadOnlySpan)
			timeSpent := readOnlySpan.EndTime().Sub(readOnlySpan.StartTime())
			cron.metricsJobSpentDuration.Record(ctx, timeSpent.Seconds(), metricsAttributeSetOpt)
		}()

		return handler(ctx)
	}
}
